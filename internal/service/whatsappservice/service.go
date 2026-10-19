package whatsappservice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

type WhatsApp interface {
	Status() domain.Status
	Login(ctx context.Context, phone string) (domain.Status, error)
	Logout(ctx context.Context) error
	ResolveRecipient(ctx context.Context, to string) (string, error)
	CanonicalChat(ctx context.Context, jid string) (string, error)
	ChatAliases(ctx context.Context, jid string) ([]string, error)
	CheckNumbers(ctx context.Context, phones []string) ([]domain.NumberCheck, error)
	Contacts(ctx context.Context) ([]domain.Contact, error)
	SendText(ctx context.Context, chatJID, text string, quoted *domain.Message) (domain.SentMessage, error)
	MarkRead(ctx context.Context, chatJID string, msgs []domain.Message) error
}

type MessagesRepository interface {
	UpsertChat(ctx context.Context, chat domain.Chat) error
	RenameChat(ctx context.Context, jid, name string) error
	GetChat(ctx context.Context, jid string) (domain.Chat, bool, error)
	ListChats(ctx context.Context, req *messagesrepo.ListChatsRequest) ([]domain.Chat, error)
	SaveMessages(ctx context.Context, msgs []domain.Message) error
	UpdateMessageText(ctx context.Context, chatJIDs []string, id, text string) error
	GetMessage(ctx context.Context, chatJIDs []string, id string) (domain.Message, error)
	ListMessages(ctx context.Context, req *messagesrepo.ListMessagesRequest) ([]domain.Message, error)
	LastSentAt(ctx context.Context, chatJIDs []string) (time.Time, error)
}

type Service struct {
	whatsapp WhatsApp
	messages MessagesRepository
	broker   *broker
}

func New(whatsapp WhatsApp, messages MessagesRepository) *Service {
	return &Service{
		whatsapp: whatsapp,
		messages: messages,
		broker:   newBroker(),
	}
}

func (s *Service) HandleUpdate(ctx context.Context, u whatsappprovider.Update) {
	for _, chat := range u.Chats {
		if err := s.messages.UpsertChat(ctx, chat); err != nil {
			logctx.Error(ctx, "failed to store chat", zap.String("chat_jid", chat.JID), zap.Error(err))
		}
	}
	if err := s.messages.SaveMessages(ctx, u.Messages); err != nil {
		logctx.Error(ctx, "failed to store messages", zap.Int("count", len(u.Messages)), zap.Error(err))
	}
	for _, chat := range u.Renames {
		if err := s.messages.RenameChat(ctx, chat.JID, chat.Name); err != nil {
			logctx.Error(ctx, "failed to rename chat", zap.String("chat_jid", chat.JID), zap.Error(err))
		}
	}
	for _, edit := range u.Edits {
		aliases, err := s.whatsapp.ChatAliases(ctx, edit.ChatJID)
		if err != nil {
			aliases = []string{edit.ChatJID}
		}
		if err := s.messages.UpdateMessageText(ctx, aliases, edit.MessageID, edit.Text); err != nil {
			logctx.Error(ctx, "failed to apply message edit",
				zap.String("chat_jid", edit.ChatJID), zap.String("message_id", edit.MessageID), zap.Error(err))
		}
	}
	if u.Live {
		for _, m := range u.Messages {
			if !m.FromMe {
				s.broker.publish(m)
			}
		}
	}
}

func (s *Service) resolveChat(ctx context.Context, chat string) (canonical string, aliases []string, err error) {
	chat = strings.TrimSpace(chat)
	if chat == "" {
		return "", nil, domain.ErrInvalidRecipient
	}
	canonical, err = s.whatsapp.ResolveRecipient(ctx, chat)
	if err != nil {
		if strings.Contains(chat, "@") || !(errors.Is(err, domain.ErrNotConnected) || errors.Is(err, domain.ErrNotLoggedIn)) {
			return "", nil, err
		}
		digits, perr := whatsappprovider.NormalizePhone(chat)
		if perr != nil {
			return "", nil, perr
		}
		canonical = digits + "@s.whatsapp.net"
	}
	aliases, err = s.whatsapp.ChatAliases(ctx, canonical)
	if err != nil {
		return "", nil, err
	}
	return canonical, aliases, nil
}
