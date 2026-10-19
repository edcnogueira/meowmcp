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
		return GetMediaResponse{}, domain.ErrNoMedia
	}
	mimeType, err := whatsappprovider.MediaMimeType(ref)
	if err != nil {
		return GetMediaResponse{}, err
	}
	baseType := baseMimeType(mimeType)

	path, size, err := s.mediaFile(ctx, msg, ref, baseType)
	if err != nil {
		return GetMediaResponse{}, err
	}
	resp := GetMediaResponse{Message: msg, Path: path, MimeType: baseType, Size: size}
	if strings.HasPrefix(baseType, "audio/") && (msg.Transcript != "" || req.Transcribe) {
		resp.Transcript, resp.TranscriptionError = s.transcript(ctx, msg, aliases, path)
		resp.Message.Transcript = resp.Transcript
	}
	return resp, nil
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

func (s *Service) transcript(ctx context.Context, msg domain.Message, aliases []string, path string) (text, errText string) {
	if msg.Transcript != "" {
		return msg.Transcript, ""
	}
	if s.transcriber == nil {
		return "", errTranscriptionDisabled
	}
	text, err := s.transcriber.Transcribe(ctx, path)
	if err != nil {
		logctx.Warn(ctx, "failed to transcribe audio",
			zap.String("chat_jid", msg.ChatJID), zap.String("message_id", msg.ID), zap.Error(err))
		return "", err.Error()
	}
	if text == "" {
		return "", "no speech recognized"
	}
	if err := s.messages.SetTranscript(ctx, aliases, msg.ID, text); err != nil {
		logctx.Error(ctx, "failed to store transcript",
			zap.String("chat_jid", msg.ChatJID), zap.String("message_id", msg.ID), zap.Error(err))
	}
	return text, ""
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
