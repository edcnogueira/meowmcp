package mcptransport

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type Service interface {
	Status() domain.Status
	SendApprovalRequired() bool
	Login(ctx context.Context, req *whatsappservice.LoginRequest) (domain.Status, error)
	Logout(ctx context.Context) error
	ListChats(ctx context.Context, req *whatsappservice.ListChatsRequest) ([]domain.Chat, error)
	GetMessages(ctx context.Context, req *whatsappservice.GetMessagesRequest) (whatsappservice.GetMessagesResponse, error)
	ChatState(ctx context.Context, chat string) (whatsappservice.ChatStateResponse, error)
	SearchMessages(ctx context.Context, req *whatsappservice.SearchMessagesRequest) ([]domain.Message, error)
	SearchContacts(ctx context.Context, req *whatsappservice.SearchContactsRequest) ([]domain.Contact, error)
	CheckNumbers(ctx context.Context, phones []string) ([]domain.NumberCheck, error)
	SendMessage(ctx context.Context, req *whatsappservice.SendMessageRequest) (whatsappservice.SendMessageResponse, error)
	SendFile(ctx context.Context, req *whatsappservice.SendFileRequest) (whatsappservice.SendFileResponse, error)
	WaitForMessages(ctx context.Context, req *whatsappservice.WaitForMessagesRequest) (whatsappservice.WaitForMessagesResponse, error)
	MarkRead(ctx context.Context, chat string) (int, error)
	GetMedia(ctx context.Context, req *whatsappservice.GetMediaRequest) (whatsappservice.GetMediaResponse, error)
	ResolveChat(ctx context.Context, chat string) (domain.Chat, []string, error)
	MessagesSince(ctx context.Context, aliases []string, sinceID int64, limit int) ([]domain.Message, error)
	LatestID(ctx context.Context, aliases []string) (int64, error)
	Watch(aliases []string) (<-chan struct{}, func())
}

const instructions = `WhatsApp access to the user's own personal account (messages are sent as the user).

Session: call get_status first. If state is "logged_out", call login and show the user the QR code
(image, qr_image_path, or ascii_qr) to scan in WhatsApp > Linked devices > Link a device. If the user
cannot scan, call login with their phone number and give them the 8-character pairing_code to type in
WhatsApp > Linked devices > Link with phone number instead. Poll get_status until state is "connected".

Reading: list_chats (optionally with query) or search_contacts to find a chat JID. At the start of a turn
about a conversation call get_chat_state: last incoming and outgoing message, what is unanswered and whether
attachments are ready. get_messages pages history; pass since_id (a message seq or next_since_id) to read only
what arrived after it. Nothing here sends read receipts: every read result says marked_read false, and only
mark_as_read does. Attachments carry media_status: ready (stored), downloadable, or missing_key (cannot be
fetched; ask the sender to resend). View-once photos/videos are opened and stored as they arrive; get_media
returns the stored copy. A voice transcript with transcript_unclear true is unreliable: do not fill in words.
Mentions list who each @tag in text is; is_me marks the user.

Waiting: do not hold a turn waiting for an answer. Subscribe to the chat.message_received event with the chat
as argument; the next run starts with the new messages. wait_for_reply is only for answers expected within
seconds (bots, OTP codes).

Writing: send_message accepts a phone number with country code or a JID; to start a new conversation just
send to the phone number. send_file sends photos, videos, audio, voice notes and documents to the same
recipients, from a file_path on the server host or base64 data. The result echoes exactly what went out.

Safety: messages go out under the user's name. Confirm the recipient and the exact text (or file) with the
user before calling send_message or send_file. When get_status reports send_approval "required", the server
refuses any send without an approval token that the user's client issues once the user approves that exact
recipient and content; never invent or reuse one. Never share the user's private information beyond what the
task requires.`

type Options struct {
	QRImagePath string
	Version     string
	Webhooks    WebhookSender
}

type Transport struct {
	service     Service
	qrImagePath string
	version     string
	events      *eventHub
	stop        context.CancelFunc
}

func New(service Service, opts Options) *Transport {
	ctx, stop := context.WithCancel(context.Background())
	t := &Transport{service: service, qrImagePath: opts.QRImagePath, version: opts.Version, stop: stop}
	if opts.Webhooks != nil {
		t.events = newEventHub(ctx, service, opts.Webhooks)
	}
	return t
}

func (t *Transport) Close() {
	t.stop()
}

func (t *Transport) Server() (*mcp.Server, error) {
	return t.server(true)
}

func (t *Transport) server(authenticated bool) (*mcp.Server, error) {
	opts := &mcp.ServerOptions{Instructions: instructions}
	if t.events != nil {
		opts.Capabilities = &mcp.ServerCapabilities{
			Logging:      &mcp.LoggingCapabilities{},
			Experimental: map[string]any{"events": map[string]any{}},
		}
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "meowmcp",
		Title:   "WhatsApp",
		Version: t.version,
	}, opts)
	t.registerSessionTools(server)
	t.registerReadTools(server)
	t.registerWriteTools(server)
	if t.events != nil {
		if err := t.events.register(server, authenticated); err != nil {
			return nil, fmt.Errorf("failed to register event methods: %w", err)
		}
	}
	return server, nil
}

func (t *Transport) HTTPHandler(token string) (http.Handler, error) {
	server, err := t.server(token != "")
	if err != nil {
		return nil, err
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(t.service.Status().State.String()))
	})
	mux.Handle("/mcp", requireBearer(token, mcpHandler))
	return mux, nil
}

func requireBearer(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="meowmcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseTime(field, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	if d, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil {
		return d, nil
	}
	return time.Time{}, &inputError{field: field, msg: "must be an RFC 3339 timestamp (2026-10-19T14:30:00-03:00) or a date (2026-10-19)"}
}

type inputError struct {
	field string
	msg   string
}

func (e *inputError) Error() string { return e.field + " " + e.msg }

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.In(time.Local).Format(time.RFC3339)
}
