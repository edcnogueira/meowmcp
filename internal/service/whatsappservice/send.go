package whatsappservice

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

const maxSendFileBytes = 100 << 20

var errTooLarge = fmt.Errorf("file is larger than %d MB", maxSendFileBytes>>20)

var extensionMimeTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".gif":  "image/gif",
	".heic": "image/heic",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".amr":  "audio/amr",
	".wav":  "audio/wav",
	".mp4":  "video/mp4",
	".3gp":  "video/3gpp",
	".mov":  "video/quicktime",
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	".txt":  "text/plain",
	".csv":  "text/csv",
	".json": "application/json",
	".zip":  "application/zip",
	".ics":  "text/calendar",
	".vcf":  "text/vcard",
}

var nativeMimeTypes = map[domain.MediaKind][]string{
	domain.MediaKindImage: {"image/jpeg", "image/png"},
	domain.MediaKindVideo: {"video/mp4", "video/3gpp"},
	domain.MediaKindVoice: {"audio/ogg"},
}

type SendMessageRequest struct {
	To        string
	Text      string
	ReplyToID string
}

type SendMessageResponse struct {
	Sent domain.SentMessage
	Chat domain.Chat
}

type SendFileRequest struct {
	To        string
	Path      string
	Data      []byte
	FileName  string
	Caption   string
	SendAs    string
	ReplyToID string
}

type SendFileResponse struct {
	Sent     domain.SentMessage
	Chat     domain.Chat
	MimeType string
	Size     int64
}

func (s *Service) SendMessage(ctx context.Context, req *SendMessageRequest) (SendMessageResponse, error) {
	if err := validateSendMessageRequest(req); err != nil {
		return SendMessageResponse{}, err
	}
	return s.send(ctx, req.To, req.ReplyToID, func(chatJID string, quoted *domain.Message) (domain.SentMessage, error) {
		return s.whatsapp.SendText(ctx, chatJID, req.Text, quoted)
	})
}

func (s *Service) SendFile(ctx context.Context, req *SendFileRequest) (SendFileResponse, error) {
	if err := validateSendFileRequest(req); err != nil {
		return SendFileResponse{}, err
	}
	media, err := outgoingMedia(req)
	if err != nil {
		return SendFileResponse{}, err
	}
	resp, err := s.send(ctx, req.To, req.ReplyToID, func(chatJID string, quoted *domain.Message) (domain.SentMessage, error) {
		return s.whatsapp.SendMedia(ctx, chatJID, media, quoted)
	})
	if err != nil {
		return SendFileResponse{}, err
	}
	return SendFileResponse{Sent: resp.Sent, Chat: resp.Chat, MimeType: media.MimeType, Size: int64(len(media.Data))}, nil
}

func (s *Service) send(ctx context.Context, to, replyToID string, deliver func(chatJID string, quoted *domain.Message) (domain.SentMessage, error)) (SendMessageResponse, error) {
	chatJID, err := s.whatsapp.ResolveRecipient(ctx, to)
	if err != nil {
		return SendMessageResponse{}, err
	}
	aliases, err := s.whatsapp.ChatAliases(ctx, chatJID)
	if err != nil {
		return SendMessageResponse{}, err
	}

	var quoted *domain.Message
	if replyToID != "" {
		m, err := s.messages.GetMessage(ctx, aliases, replyToID)
		if err != nil {
			return SendMessageResponse{}, fmt.Errorf("reply_to_id %s: %w", replyToID, err)
		}
		quoted = &m
	}

	sent, err := deliver(chatJID, quoted)
	if err != nil {
		return SendMessageResponse{}, err
	}

	st := s.whatsapp.Status()
	msg := domain.Message{
		ID:         sent.ID,
		ChatJID:    sent.ChatJID,
		SenderJID:  st.JID,
		SenderName: st.PushName,
		FromMe:     true,
		Timestamp:  sent.Timestamp,
		Text:       sent.Text,
		MediaType:  sent.MediaType,
		QuotedID:   replyToID,
		MediaRef:   sent.MediaRef,
	}
	if err := s.messages.UpsertChat(ctx, domain.Chat{
		JID:           sent.ChatJID,
		IsGroup:       strings.HasSuffix(sent.ChatJID, "@g.us"),
		LastMessageAt: sent.Timestamp,
	}); err != nil {
		logctx.Error(ctx, "failed to record chat of sent message", zap.String("chat_jid", sent.ChatJID), zap.Error(err))
	}
	if err := s.messages.SaveMessages(ctx, []domain.Message{msg}); err != nil {
		logctx.Error(ctx, "failed to record sent message", zap.String("message_id", sent.ID), zap.Error(err))
	}

	chat, err := s.chatInfo(ctx, sent.ChatJID, aliases)
	if err != nil {
		return SendMessageResponse{}, err
	}
	return SendMessageResponse{Sent: sent, Chat: chat}, nil
}

func outgoingMedia(req *SendFileRequest) (domain.OutgoingMedia, error) {
	data, fileName := req.Data, strings.TrimSpace(req.FileName)
	if path := strings.TrimSpace(req.Path); path != "" {
		var err error
		if data, err = readSendFile(path); err != nil {
			return domain.OutgoingMedia{}, err
		}
		if fileName == "" {
			fileName = filepath.Base(path)
		}
	}
	if len(data) == 0 {
		return domain.OutgoingMedia{}, errors.New("file is empty")
	}
	if len(data) > maxSendFileBytes {
		return domain.OutgoingMedia{}, errTooLarge
	}

	mimeType := detectMimeType(fileName, data)
	kind, err := mediaKind(strings.ToLower(strings.TrimSpace(req.SendAs)), mimeType)
	if err != nil {
		return domain.OutgoingMedia{}, err
	}
	caption := strings.TrimSpace(req.Caption)
	if caption != "" && (kind == domain.MediaKindAudio || kind == domain.MediaKindVoice) {
		return domain.OutgoingMedia{}, errors.New("caption is not supported for audio and voice; send the text with send_message")
	}
	return domain.OutgoingMedia{Kind: kind, MimeType: mimeType, FileName: fileName, Caption: caption, Data: data}, nil
}

func readSendFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("file_path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file_path %s is not a regular file", path)
	}
	if info.Size() > maxSendFileBytes {
		return nil, errTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

func detectMimeType(fileName string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if mimeType, ok := extensionMimeTypes[ext]; ok {
		return mimeType
	}
	if mimeType := mime.TypeByExtension(ext); ext != "" && mimeType != "" {
		return baseMimeType(mimeType)
	}
	return baseMimeType(http.DetectContentType(data))
}

func mediaKind(sendAs, mimeType string) (domain.MediaKind, error) {
	switch kind := domain.MediaKind(sendAs); kind {
	case "":
		for _, auto := range []domain.MediaKind{domain.MediaKindImage, domain.MediaKindVideo} {
			if slices.Contains(nativeMimeTypes[auto], mimeType) {
				return auto, nil
			}
		}
		if strings.HasPrefix(mimeType, "audio/") {
			return domain.MediaKindAudio, nil
		}
		return domain.MediaKindDocument, nil
	case domain.MediaKindDocument:
		return kind, nil
	case domain.MediaKindAudio:
		if !strings.HasPrefix(mimeType, "audio/") {
			return "", fmt.Errorf("send_as audio needs an audio file, got %s; use send_as document", mimeType)
		}
		return kind, nil
	case domain.MediaKindImage, domain.MediaKindVideo, domain.MediaKindVoice:
		if !slices.Contains(nativeMimeTypes[kind], mimeType) {
			return "", fmt.Errorf("send_as %s needs %s, got %s; use send_as document", kind, strings.Join(nativeMimeTypes[kind], " or "), mimeType)
		}
		return kind, nil
	}
	return "", fmt.Errorf("send_as must be image, video, audio, voice or document, got %q", sendAs)
}

func validateSendMessageRequest(req *SendMessageRequest) error {
	var missing []string
	if strings.TrimSpace(req.To) == "" {
		missing = append(missing, "to")
	}
	if strings.TrimSpace(req.Text) == "" {
		missing = append(missing, "text")
	}
	if len(missing) > 0 {
		return errors.New("required: " + strings.Join(missing, ", "))
	}
	return nil
}

func validateSendFileRequest(req *SendFileRequest) error {
	if strings.TrimSpace(req.To) == "" {
		return errors.New("required: to")
	}
	hasPath, hasData := strings.TrimSpace(req.Path) != "", len(req.Data) > 0
	switch {
	case hasPath == hasData:
		return errors.New("provide exactly one of file_path or data_base64")
	case hasData && strings.TrimSpace(req.FileName) == "":
		return errors.New("required with data_base64: file_name")
	}
	return nil
}
