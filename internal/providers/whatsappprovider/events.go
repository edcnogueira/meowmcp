package whatsappprovider

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

const deletedMessageText = "[message deleted]"

func (p *Provider) handleEvent(cli *whatsmeow.Client, evt any) {
	ctx := p.runCtx
	switch e := evt.(type) {
	case *events.Connected:
		p.setState(cli, domain.ConnectionStateConnected, "")
		p.logger.Info("whatsapp connected", zap.String("jid", cli.Store.ID.ToNonAD().String()))
		go p.syncGroupNames(ctx, cli)
	case *events.Disconnected:
		if cli.Store.ID != nil {
			p.setState(cli, domain.ConnectionStateConnecting, "")
		}
	case *events.LoggedOut:
		p.setState(cli, domain.ConnectionStateLoggedOut,
			fmt.Sprintf("device was unlinked (%s); log in again to pair", e.Reason.String()))
	case *events.StreamReplaced:
		p.setState(cli, domain.ConnectionStateDisconnected,
			"another process connected with this session; only one meowmcp instance may run per session")
	case *events.TemporaryBan:
		p.setState(cli, domain.ConnectionStateDisconnected, "temporarily banned by whatsapp: "+e.String())
	case *events.ClientOutdated:
		p.setState(cli, domain.ConnectionStateDisconnected, "whatsapp rejected the client as outdated; update go.mau.fi/whatsmeow")
	case *events.ConnectFailure:
		p.setLastErr(fmt.Sprintf("connect failure: %s %s", e.Reason.String(), e.Message))
	case *events.Message:
		p.handleMessage(ctx, cli, e)
	case *events.HistorySync:
		p.handleHistorySync(ctx, cli, e)
	case *events.PushName:
		p.handleRename(ctx, cli, e.JID, e.NewPushName)
	case *events.Contact:
		p.handleRename(ctx, cli, e.JID, e.Action.GetFullName())
	case *events.GroupInfo:
		if e.Name != nil && e.Name.Name != "" {
			p.onUpdate(ctx, Update{Live: true, Renames: []domain.Chat{{JID: e.JID.ToNonAD().String(), Name: e.Name.Name, IsGroup: true}}})
		}
	}
}

func (p *Provider) handleMessage(ctx context.Context, cli *whatsmeow.Client, e *events.Message) {
	if skipChat(e.Info.Chat) {
		return
	}
	chat := p.chatOf(ctx, cli, e.Info.Chat)
	u := Update{Live: true, Chats: []domain.Chat{p.chatEntry(ctx, cli, chat, e)}}

	msg, edit, ok := p.convertMessage(ctx, cli, chat, e)
	if ok {
		u.Messages = append(u.Messages, msg)
	}
	if edit != nil {
		u.Edits = append(u.Edits, *edit)
	}
	if !ok && edit == nil {
		return
	}
	if chat.Server == types.GroupServer {
		p.ensureGroupName(ctx, cli, chat)
	}
	p.onUpdate(ctx, u)
}

func (p *Provider) chatEntry(ctx context.Context, cli *whatsmeow.Client, chat types.JID, e *events.Message) domain.Chat {
	entry := domain.Chat{JID: chat.String(), IsGroup: chat.Server == types.GroupServer, LastMessageAt: e.Info.Timestamp}
	if !entry.IsGroup {
		fallback := ""
		if !e.Info.IsFromMe {
			fallback = e.Info.PushName
		}
		entry.Name = p.contactName(ctx, cli, chat, fallback)
	}
	return entry
}

func (p *Provider) handleHistorySync(ctx context.Context, cli *whatsmeow.Client, e *events.HistorySync) {
	var u Update
	for _, conv := range e.Data.GetConversations() {
		rawJID, err := types.ParseJID(conv.GetID())
		if err != nil || skipChat(rawJID) {
			continue
		}
		chat := p.chatOf(ctx, cli, rawJID)
		isGroup := chat.Server == types.GroupServer
		name := conv.GetName()
		if name == "" {
			name = conv.GetDisplayName()
		}
		if name == "" && !isGroup {
			name = p.contactName(ctx, cli, chat, "")
		}
		entry := domain.Chat{JID: chat.String(), Name: name, IsGroup: isGroup}
		if ts := max(conv.GetConversationTimestamp(), conv.GetLastMsgTimestamp()); ts > 0 {
			entry.LastMessageAt = time.Unix(int64(ts), 0)
		}
		u.Chats = append(u.Chats, entry)

		for _, hm := range conv.GetMessages() {
			evt, err := cli.ParseWebMessage(rawJID, hm.GetMessage())
			if err != nil {
				p.logger.Debug("skipping unparsable history message", zap.String("chat", rawJID.String()), zap.Error(err))
				continue
			}
			msg, edit, ok := p.convertMessage(ctx, cli, chat, evt)
			if ok {
				u.Messages = append(u.Messages, msg)
			}
			if edit != nil {
				u.Edits = append(u.Edits, *edit)
			}
		}
	}
	p.logger.Info("history sync received",
		zap.String("type", e.Data.GetSyncType().String()),
		zap.Int("chats", len(u.Chats)),
		zap.Int("messages", len(u.Messages)))
	if len(u.Chats) > 0 || len(u.Messages) > 0 {
		p.onUpdate(ctx, u)
	}
}

func (p *Provider) convertMessage(ctx context.Context, cli *whatsmeow.Client, chat types.JID, e *events.Message) (msg domain.Message, edit *Edit, ok bool) {
	if pm := e.Message.GetProtocolMessage(); pm != nil {
		switch pm.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			return domain.Message{}, &Edit{ChatJID: chat.String(), MessageID: pm.GetKey().GetID(), Text: deletedMessageText}, false
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			c := ExtractContent(pm.GetEditedMessage())
			return domain.Message{}, &Edit{ChatJID: chat.String(), MessageID: pm.GetKey().GetID(), Text: c.Text}, false
		}
		return domain.Message{}, nil, false
	}

	c := ExtractContent(e.Message)
	if c.IsEmpty() {
		return domain.Message{}, nil, false
	}
	sender := p.canonical(ctx, cli, e.Info.Sender)
	senderName := e.Info.PushName
	if e.Info.IsFromMe {
		senderName = cli.Store.PushName
	} else {
		senderName = p.contactName(ctx, cli, sender, senderName)
	}
	return domain.Message{
		ID:         e.Info.ID,
		ChatJID:    chat.String(),
		SenderJID:  sender.String(),
		SenderName: senderName,
		FromMe:     e.Info.IsFromMe,
		Timestamp:  e.Info.Timestamp,
		Text:       c.Text,
		MediaType:  c.MediaType,
		QuotedID:   c.QuotedID,
	}, nil, true
}

func (p *Provider) handleRename(ctx context.Context, cli *whatsmeow.Client, jid types.JID, fallback string) {
	chat := p.canonical(ctx, cli, jid)
	if name := p.contactName(ctx, cli, chat, fallback); name != "" {
		p.onUpdate(ctx, Update{Live: true, Renames: []domain.Chat{{JID: chat.String(), Name: name}}})
	}
}

func (p *Provider) syncGroupNames(ctx context.Context, cli *whatsmeow.Client) {
	groups, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		p.logger.Warn("failed to fetch joined groups", zap.Error(err))
		return
	}
	renames := make([]domain.Chat, 0, len(groups))
	p.mu.Lock()
	for _, g := range groups {
		p.groupNames[g.JID] = struct{}{}
		if g.Name != "" {
			renames = append(renames, domain.Chat{JID: g.JID.String(), Name: g.Name, IsGroup: true})
		}
	}
	p.mu.Unlock()
	if len(renames) > 0 {
		p.onUpdate(ctx, Update{Live: true, Renames: renames})
	}
}

func (p *Provider) ensureGroupName(ctx context.Context, cli *whatsmeow.Client, group types.JID) {
	p.mu.Lock()
	_, seen := p.groupNames[group]
	p.groupNames[group] = struct{}{}
	p.mu.Unlock()
	if seen {
		return
	}
	go func() {
		info, err := cli.GetGroupInfo(ctx, group)
		if err != nil {
			p.logger.Debug("failed to fetch group info", zap.String("group", group.String()), zap.Error(err))
			return
		}
		if info.Name != "" {
			p.onUpdate(ctx, Update{Live: true, Renames: []domain.Chat{{JID: group.String(), Name: info.Name, IsGroup: true}}})
		}
	}()
}

func (p *Provider) chatOf(ctx context.Context, cli *whatsmeow.Client, jid types.JID) types.JID {
	if jid.Server == types.GroupServer {
		return jid.ToNonAD()
	}
	return p.canonical(ctx, cli, jid)
}

func (p *Provider) canonical(ctx context.Context, cli *whatsmeow.Client, jid types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server == types.HiddenUserServer && cli.Store.LIDs != nil {
		if pn, err := cli.Store.LIDs.GetPNForLID(ctx, jid); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD()
		}
	}
	return jid
}

func (p *Provider) contactName(ctx context.Context, cli *whatsmeow.Client, jid types.JID, fallback string) string {
	if cli.Store.Contacts == nil {
		return fallback
	}
	for _, candidate := range p.aliasJIDs(ctx, cli, jid) {
		ci, err := cli.Store.Contacts.GetContact(ctx, candidate)
		if err != nil || !ci.Found {
			continue
		}
		for _, name := range []string{ci.FullName, ci.FirstName, ci.BusinessName, ci.PushName} {
			if name != "" {
				return name
			}
		}
	}
	return fallback
}

func (p *Provider) aliasJIDs(ctx context.Context, cli *whatsmeow.Client, jid types.JID) []types.JID {
	jid = jid.ToNonAD()
	out := []types.JID{jid}
	if cli.Store.LIDs == nil {
		return out
	}
	var (
		alt types.JID
		err error
	)
	switch jid.Server {
	case types.DefaultUserServer:
		alt, err = cli.Store.LIDs.GetLIDForPN(ctx, jid)
	case types.HiddenUserServer:
		alt, err = cli.Store.LIDs.GetPNForLID(ctx, jid)
	default:
		return out
	}
	if err == nil && !alt.IsEmpty() {
		out = append(out, alt.ToNonAD())
	}
	return out
}

func skipChat(jid types.JID) bool {
	return jid == types.StatusBroadcastJID || jid.Server == types.NewsletterServer || jid.Server == types.BroadcastServer
}
