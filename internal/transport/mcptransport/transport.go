package mcptransport

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type Service interface {
	Status() domain.Status
	Login(ctx context.Context, req *whatsappservice.LoginRequest) (domain.Status, error)
	Logout(ctx context.Context) error
	ListChats(ctx context.Context, req *whatsappservice.ListChatsRequest) ([]domain.Chat, error)
	GetMessages(ctx context.Context, req *whatsappservice.GetMessagesRequest) (whatsappservice.GetMessagesResponse, error)
	SearchMessages(ctx context.Context, req *whatsappservice.SearchMessagesRequest) ([]domain.Message, error)
	SearchContacts(ctx context.Context, req *whatsappservice.SearchContactsRequest) ([]domain.Contact, error)
	CheckNumbers(ctx context.Context, phones []string) ([]domain.NumberCheck, error)
	SendMessage(ctx context.Context, req *whatsappservice.SendMessageRequest) (whatsappservice.SendMessageResponse, error)
	WaitForMessages(ctx context.Context, req *whatsappservice.WaitForMessagesRequest) (whatsappservice.WaitForMessagesResponse, error)
	MarkRead(ctx context.Context, chat string) (int, error)
	GetMedia(ctx context.Context, req *whatsappservice.GetMediaRequest) (whatsappservice.GetMediaResponse, error)
}

const instructions = `WhatsApp access to the user's own personal account (messages are sent as the user).

Session: call get_status first. If state is "logged_out", call login and show the user the QR code
(image, qr_image_path, or ascii_qr) to scan in WhatsApp > Linked devices > Link a device. If the user
cannot scan, call login with their phone number and give them the 8-character pairing_code to type in
WhatsApp > Linked devices > Link with phone number instead. Poll get_status until state is "connected".

Reading: list_chats (optionally with query) or search_contacts to find a chat JID, then get_messages.
Messages with media_type image, sticker, voice, audio, video or document carry only a caption/file name;
call get_media with the chat and message id to see the image; for a voice note, pass transcribe=true only
when its spoken content is needed for the task.
Writing: send_message accepts a phone number with country code or a JID; to start a new conversation
just send to the phone number. After sending, call wait_for_reply with the same chat to block until the
other side answers, then continue the conversation.

Safety: messages go out under the user's name. Unless the user explicitly delegated the conversation,
confirm the recipient and the exact text with the user before calling send_message. Never share the
user's private information beyond what the task requires.`

type Transport struct {
	service     Service
	qrImagePath string
	version     string
}

func New(service Service, qrImagePath, version string) *Transport {
	return &Transport{service: service, qrImagePath: qrImagePath, version: version}
}

func (t *Transport) Server() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "meowmcp",
		Title:   "WhatsApp",
		Version: t.version,
	}, &mcp.ServerOptions{Instructions: instructions})
	t.registerSessionTools(server)
	t.registerReadTools(server)
	t.registerWriteTools(server)
	return server
}

func (t *Transport) HTTPHandler(token string) http.Handler {
	server := t.Server()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(t.service.Status().State.String()))
	})
	mux.Handle("/mcp", requireBearer(token, mcpHandler))
	return mux
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
