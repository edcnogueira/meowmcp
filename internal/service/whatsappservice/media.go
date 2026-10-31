package whatsappservice

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
)

const errTranscriptionDisabled = "transcription is not configured on the server (set MEOWMCP_STT_API_KEY)"

var mediaExtensions = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/webp":      ".webp",
	"image/gif":       ".gif",
	"audio/ogg":       ".ogg",
	"audio/mpeg":      ".mp3",
	"audio/mp4":       ".m4a",
	"audio/aac":       ".aac",
	"audio/amr":       ".amr",
	"audio/wav":       ".wav",
	"video/mp4":       ".mp4",
	"application/pdf": ".pdf",
}

type GetMediaRequest struct {
	Chat       string
	MessageID  string
	Transcribe bool
}

type GetMediaResponse struct {
	Message            domain.Message
	Path               string
	MimeType           string
	Size               int64
	Transcript         string
	TranscriptUnclear  bool
	TranscriptionError string
}

func (s *Service) GetMedia(ctx context.Context, req *GetMediaRequest) (GetMediaResponse, error) {
	_, aliases, err := s.resolveChat(ctx, req.Chat)
	if err != nil {
		return GetMediaResponse{}, err
	}
	msg, err := s.messages.GetMedia(ctx, aliases, strings.TrimSpace(req.MessageID))
	if err != nil {
		return GetMediaResponse{}, err
	}
	ref := msg.MediaRef
	msg.MediaRef = nil
	if len(ref) == 0 {
		if mediaStatus(msg) == domain.MediaStatusReady {
			info, err := os.Stat(msg.MediaFile)
			if err != nil {
				return GetMediaResponse{}, fmt.Errorf("failed to stat media file: %w", err)
			}
			msg.MediaStatus = domain.MediaStatusReady
			mimeType := baseMimeType(mime.TypeByExtension(filepath.Ext(msg.MediaFile)))
			return GetMediaResponse{Message: msg, Path: msg.MediaFile, MimeType: mimeType, Size: info.Size()}, nil
		}
		if domain.IsDownloadableMediaType(msg.MediaType) {
			return GetMediaResponse{}, domain.ErrMediaMissingKey
		}
		return GetMediaResponse{}, domain.ErrNoMedia
	}
	baseType, err := refMimeType(ref)
	if err != nil {
		return GetMediaResponse{}, err
	}

	path, size, err := s.storeMedia(ctx, msg, aliases, ref, baseType)
	if err != nil {
		return GetMediaResponse{}, err
	}
	resp := GetMediaResponse{Message: msg, Path: path, MimeType: baseType, Size: size}
	if strings.HasPrefix(baseType, "audio/") && (msg.Transcript != "" || req.Transcribe) {
		t, errText := s.transcript(ctx, msg, aliases, path)
		resp.Transcript, resp.TranscriptUnclear, resp.TranscriptionError = t.Text, t.Unclear, errText
		resp.Message.Transcript, resp.Message.TranscriptUnclear = t.Text, t.Unclear
	}
	resp.Message.MediaFile = path
	resp.Message.MediaStatus = domain.MediaStatusReady
	return resp, nil
}

func (s *Service) storeViewOnce(ctx context.Context, msg domain.Message) {
	aliases, err := s.whatsapp.ChatAliases(ctx, msg.ChatJID)
	if err != nil {
		aliases = []string{msg.ChatJID}
	}
	baseType, err := refMimeType(msg.MediaRef)
	if err == nil {
		_, _, err = s.storeMedia(ctx, msg, aliases, msg.MediaRef, baseType)
	}
	if err != nil {
		logctx.Error(ctx, "failed to store view-once media",
			zap.String("chat_jid", msg.ChatJID), zap.String("message_id", msg.ID), zap.Error(err))
	}
}

func refMimeType(ref []byte) (string, error) {
	mimeType, err := whatsappprovider.MediaMimeType(ref)
	if err != nil {
		return "", err
	}
	return baseMimeType(mimeType), nil
}

func (s *Service) storeMedia(ctx context.Context, msg domain.Message, aliases []string, ref []byte, baseType string) (string, int64, error) {
	path, size, err := s.mediaFile(ctx, msg, ref, baseType)
	if err != nil {
		return "", 0, err
	}
	if msg.MediaFile != path {
		if err := s.messages.SetMediaFile(ctx, aliases, msg.ID, path); err != nil {
			logctx.Error(ctx, "failed to record media file",
				zap.String("chat_jid", msg.ChatJID), zap.String("message_id", msg.ID), zap.Error(err))
		}
	}
	return path, size, nil
}

func (s *Service) mediaFile(ctx context.Context, msg domain.Message, ref []byte, baseType string) (string, int64, error) {
	dir := filepath.Join(s.mediaDir, fileSafe(msg.ChatJID))
	path := filepath.Join(dir, fileSafe(msg.ID)+extensionFor(baseType))
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, info.Size(), nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("failed to create media dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", 0, fmt.Errorf("failed to create media file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := s.whatsapp.DownloadMedia(ctx, ref, tmp); err != nil {
		_ = tmp.Close()
		return "", 0, err
	}
	info, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return "", 0, fmt.Errorf("failed to stat media file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("failed to write media file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", 0, fmt.Errorf("failed to store media file: %w", err)
	}
	return path, info.Size(), nil
}

func (s *Service) transcript(ctx context.Context, msg domain.Message, aliases []string, path string) (domain.Transcript, string) {
	if msg.Transcript != "" {
		return domain.Transcript{Text: msg.Transcript, Unclear: msg.TranscriptUnclear}, ""
	}
	if s.transcriber == nil {
		return domain.Transcript{}, errTranscriptionDisabled
	}
	t, err := s.transcriber.Transcribe(ctx, path)
	if err != nil {
		logctx.Warn(ctx, "failed to transcribe audio",
			zap.String("chat_jid", msg.ChatJID), zap.String("message_id", msg.ID), zap.Error(err))
		return domain.Transcript{}, err.Error()
	}
	if t.Text == "" {
		return domain.Transcript{}, "no speech recognized"
	}
	if err := s.messages.SetTranscript(ctx, aliases, msg.ID, t.Text, t.Unclear); err != nil {
		logctx.Error(ctx, "failed to store transcript",
			zap.String("chat_jid", msg.ChatJID), zap.String("message_id", msg.ID), zap.Error(err))
	}
	return t, ""
}

func baseMimeType(mimeType string) string {
	base, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		base, _, _ = strings.Cut(mimeType, ";")
	}
	return strings.ToLower(strings.TrimSpace(base))
}

func extensionFor(baseType string) string {
	if ext, ok := mediaExtensions[baseType]; ok {
		return ext
	}
	if exts, err := mime.ExtensionsByType(baseType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func fileSafe(s string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '@', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
	if strings.Trim(safe, ".") == "" {
		return "_" + safe
	}
	return safe
}
