package whatsappprovider

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

func (p *Provider) ChatAliases(ctx context.Context, jid string) ([]string, error) {
	parsed, err := ParseChatJID(jid)
	if err != nil {
		return nil, err
	}
	cli := p.currentClient()
	jids := p.aliasJIDs(ctx, cli, parsed)
	out := make([]string, len(jids))
	for i, j := range jids {
		out[i] = j.String()
	}
	return out, nil
}

func (p *Provider) CanonicalChat(ctx context.Context, jid string) (string, error) {
	parsed, err := ParseChatJID(jid)
	if err != nil {
		return "", err
	}
	return p.chatOf(ctx, p.currentClient(), parsed).String(), nil
}

func (p *Provider) ResolveRecipient(ctx context.Context, to string) (string, error) {
	to = strings.TrimSpace(to)
	if strings.Contains(to, "@") {
		return p.CanonicalChat(ctx, to)
	}
	digits, err := NormalizePhone(to)
	if err != nil {
		return "", err
	}
	checks, err := p.CheckNumbers(ctx, []string{digits})
	if err != nil {
		return "", err
	}
	if len(checks) == 0 || !checks[0].OnWhatsApp {
		return "", fmt.Errorf("%w: %s", domain.ErrNotOnWhatsApp, to)
	}
	return checks[0].JID, nil
}

func (p *Provider) CheckNumbers(ctx context.Context, phones []string) ([]domain.NumberCheck, error) {
	cli, err := p.readyClient()
	if err != nil {
		return nil, err
	}
	queries := make([]string, 0, len(phones))
	for _, ph := range phones {
		digits, err := NormalizePhone(ph)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", err, ph)
		}
		queries = append(queries, "+"+digits)
	}
	resp, err := cli.IsOnWhatsApp(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to check numbers on whatsapp: %w", err)
	}
	out := make([]domain.NumberCheck, 0, len(resp))
	for _, r := range resp {
		check := domain.NumberCheck{Query: r.Query, OnWhatsApp: r.IsIn}
		if r.IsIn {
			check.JID = r.JID.ToNonAD().String()
		}
		if r.VerifiedName != nil && r.VerifiedName.Details != nil {
			check.BusinessName = r.VerifiedName.Details.GetVerifiedName()
		}
		out = append(out, check)
	}
	return out, nil
}

func (p *Provider) Contacts(ctx context.Context) ([]domain.Contact, error) {
	cli := p.currentClient()
	if cli.Store.ID == nil {
		return nil, domain.ErrNotLoggedIn
	}
	all, err := cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load contacts: %w", err)
	}
	byJID := make(map[types.JID]domain.Contact, len(all))
	for jid, ci := range all {
		canon := p.canonical(ctx, cli, jid)
		c := byJID[canon]
		c.JID = canon.String()
		if canon.Server == types.DefaultUserServer {
			c.Phone = "+" + canon.User
		}
		c.Name = firstNonEmpty(c.Name, ci.FullName, ci.FirstName)
		c.PushName = firstNonEmpty(c.PushName, ci.PushName)
		c.BusinessName = firstNonEmpty(c.BusinessName, ci.BusinessName)
		byJID[canon] = c
	}
	out := make([]domain.Contact, 0, len(byJID))
	for _, c := range byJID {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JID < out[j].JID })
	return out, nil
}

func (p *Provider) DescribeMentions(ctx context.Context, jids []string) map[string]domain.Mention {
	cli := p.currentClient()
	var me []types.JID
	if id := cli.Store.ID; id != nil {
		me = append(me, id.ToNonAD())
	}
	if lid := cli.Store.GetLID(); !lid.IsEmpty() {
		me = append(me, lid.ToNonAD())
	}
	out := make(map[string]domain.Mention, len(jids))
	for _, raw := range jids {
		m := domain.Mention{JID: raw}
		if jid, err := types.ParseJID(raw); err == nil {
			jid = jid.ToNonAD()
			canon := p.canonical(ctx, cli, jid)
			if canon.Server == types.DefaultUserServer {
				m.Phone = "+" + canon.User
			}
			m.IsMe = slices.Contains(me, jid) || slices.Contains(me, canon)
			if m.IsMe {
				m.Name = cli.Store.PushName
			} else {
				m.Name = p.contactName(ctx, cli, canon, "")
			}
		}
		out[raw] = m
	}
	return out
}

func (p *Provider) SendText(ctx context.Context, chatJID, text string, quoted *domain.Message) (domain.SentMessage, error) {
	cli, to, err := p.sendTarget(chatJID)
	if err != nil {
		return domain.SentMessage{}, err
	}
	msg := &waE2E.Message{Conversation: proto.String(text)}
	if quoted != nil {
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(text),
			ContextInfo: quoteContext(cli, quoted),
		}}
	}
	return p.send(ctx, cli, to, msg)
}

func (p *Provider) SendMedia(ctx context.Context, chatJID string, media domain.OutgoingMedia, quoted *domain.Message) (domain.SentMessage, error) {
	appInfo, err := uploadMediaType(media.Kind)
	if err != nil {
		return domain.SentMessage{}, err
	}
	cli, to, err := p.sendTarget(chatJID)
	if err != nil {
		return domain.SentMessage{}, err
	}
	upload, err := cli.Upload(ctx, media.Data, appInfo)
	if err != nil {
		return domain.SentMessage{}, fmt.Errorf("failed to upload %s: %w", media.Kind, err)
	}
	var contextInfo *waE2E.ContextInfo
	if quoted != nil {
		contextInfo = quoteContext(cli, quoted)
	}
	return p.send(ctx, cli, to, MediaMessage(media, upload, contextInfo))
}

func (p *Provider) sendTarget(chatJID string) (*whatsmeow.Client, types.JID, error) {
	cli, err := p.readyClient()
	if err != nil {
		return nil, types.JID{}, err
	}
	to, err := ParseChatJID(chatJID)
	if err != nil {
		return nil, types.JID{}, err
	}
	return cli, to, nil
}

func (p *Provider) send(ctx context.Context, cli *whatsmeow.Client, to types.JID, msg *waE2E.Message) (domain.SentMessage, error) {
	resp, err := cli.SendMessage(ctx, to, msg)
	if err != nil {
		return domain.SentMessage{}, fmt.Errorf("failed to send message: %w", err)
	}
	content := ExtractContent(msg)
	return domain.SentMessage{
		ID:        resp.ID,
		ChatJID:   p.chatOf(ctx, cli, to).String(),
		Timestamp: resp.Timestamp,
		Text:      content.Text,
		MediaType: content.MediaType,
		MediaRef:  MediaRef(msg),
	}, nil
}

func quoteContext(cli *whatsmeow.Client, quoted *domain.Message) *waE2E.ContextInfo {
	participant := quoted.SenderJID
	if quoted.FromMe && cli.Store.ID != nil {
		participant = cli.Store.ID.ToNonAD().String()
	}
	return &waE2E.ContextInfo{
		StanzaID:      proto.String(quoted.ID),
		Participant:   proto.String(participant),
		QuotedMessage: &waE2E.Message{Conversation: proto.String(quoted.Text)},
	}
}

func (p *Provider) MarkRead(ctx context.Context, chatJID string, msgs []domain.Message) error {
	cli, err := p.readyClient()
	if err != nil {
		return err
	}
	chat, err := ParseChatJID(chatJID)
	if err != nil {
		return err
	}
	bySender := make(map[string][]types.MessageID)
	for _, m := range msgs {
		if m.FromMe {
			continue
		}
		sender := ""
		if chat.Server == types.GroupServer {
			sender = m.SenderJID
		}
		bySender[sender] = append(bySender[sender], m.ID)
	}
	for sender, ids := range bySender {
		var senderJID types.JID
		if sender != "" {
			if senderJID, err = types.ParseJID(sender); err != nil {
				return fmt.Errorf("invalid sender %q: %w", sender, err)
			}
		}
		if err := cli.MarkRead(ctx, ids, time.Now(), chat, senderJID); err != nil {
			return fmt.Errorf("failed to mark messages as read: %w", err)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
