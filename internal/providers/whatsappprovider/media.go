package whatsappprovider

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

func MediaRef(m *waE2E.Message) []byte {
	ref := &waE2E.Message{}
	switch {
	case m.GetImageMessage() != nil:
		x := proto.CloneOf(m.GetImageMessage())
		x.JPEGThumbnail, x.ContextInfo, x.Annotations = nil, nil, nil
		ref.ImageMessage = x
	case m.GetVideoMessage() != nil:
		x := proto.CloneOf(m.GetVideoMessage())
		x.JPEGThumbnail, x.ContextInfo, x.Annotations = nil, nil, nil
		ref.VideoMessage = x
	case m.GetAudioMessage() != nil:
		x := proto.CloneOf(m.GetAudioMessage())
		x.ContextInfo = nil
		ref.AudioMessage = x
	case m.GetDocumentMessage() != nil:
		x := proto.CloneOf(m.GetDocumentMessage())
		x.JPEGThumbnail, x.ContextInfo = nil, nil
		ref.DocumentMessage = x
	case m.GetStickerMessage() != nil:
		x := proto.CloneOf(m.GetStickerMessage())
		x.PngThumbnail, x.ContextInfo = nil, nil
		ref.StickerMessage = x
	default:
		return nil
	}
	b, err := proto.Marshal(ref)
	if err != nil {
		return nil
	}
	return b
}

func MediaMimeType(ref []byte) (string, error) {
	_, mimeType, err := decodeMediaRef(ref)
	return mimeType, err
}

func MediaMessage(media domain.OutgoingMedia, upload whatsmeow.UploadResponse, contextInfo *waE2E.ContextInfo) *waE2E.Message {
	caption := proto.String(media.Caption)
	if media.Caption == "" {
		caption = nil
	}
	switch media.Kind {
	case domain.MediaKindImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption:       caption,
			Mimetype:      proto.String(media.MimeType),
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileEncSHA256: upload.FileEncSHA256,
			FileSHA256:    upload.FileSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			ContextInfo:   contextInfo,
		}}
	case domain.MediaKindVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption:       caption,
			Mimetype:      proto.String(media.MimeType),
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileEncSHA256: upload.FileEncSHA256,
			FileSHA256:    upload.FileSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			ContextInfo:   contextInfo,
		}}
	case domain.MediaKindAudio, domain.MediaKindVoice:
		mimeType := media.MimeType
		if media.Kind == domain.MediaKindVoice {
			mimeType = "audio/ogg; codecs=opus"
		}
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype:      proto.String(mimeType),
			PTT:           proto.Bool(media.Kind == domain.MediaKindVoice),
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileEncSHA256: upload.FileEncSHA256,
			FileSHA256:    upload.FileSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			ContextInfo:   contextInfo,
		}}
	}
	return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		Caption:       caption,
		Title:         proto.String(media.FileName),
		FileName:      proto.String(media.FileName),
		Mimetype:      proto.String(media.MimeType),
		URL:           proto.String(upload.URL),
		DirectPath:    proto.String(upload.DirectPath),
		MediaKey:      upload.MediaKey,
		FileEncSHA256: upload.FileEncSHA256,
		FileSHA256:    upload.FileSHA256,
		FileLength:    proto.Uint64(upload.FileLength),
		ContextInfo:   contextInfo,
	}}
}

func uploadMediaType(kind domain.MediaKind) (whatsmeow.MediaType, error) {
	switch kind {
	case domain.MediaKindImage:
		return whatsmeow.MediaImage, nil
	case domain.MediaKindVideo:
		return whatsmeow.MediaVideo, nil
	case domain.MediaKindAudio, domain.MediaKindVoice:
		return whatsmeow.MediaAudio, nil
	case domain.MediaKindDocument:
		return whatsmeow.MediaDocument, nil
	}
	return "", fmt.Errorf("unsupported media kind %q", kind)
}

func (p *Provider) DownloadMedia(ctx context.Context, ref []byte, dst *os.File) error {
	msg, _, err := decodeMediaRef(ref)
	if err != nil {
		return err
	}
	cli, err := p.readyClient()
	if err != nil {
		return err
	}
	err = cli.DownloadToFile(ctx, msg, dst)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsmeow.ErrNoURLPresent),
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403),
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404),
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410):
		return fmt.Errorf("%w (%v)", domain.ErrMediaUnavailable, err)
	}
	return fmt.Errorf("failed to download media: %w", err)
}

func decodeMediaRef(ref []byte) (whatsmeow.DownloadableMessage, string, error) {
	var m waE2E.Message
	if err := proto.Unmarshal(ref, &m); err != nil {
		return nil, "", fmt.Errorf("invalid media reference: %w", err)
	}
	switch {
	case m.ImageMessage != nil:
		return m.ImageMessage, firstNonEmpty(m.ImageMessage.GetMimetype(), "image/jpeg"), nil
	case m.VideoMessage != nil:
		return m.VideoMessage, firstNonEmpty(m.VideoMessage.GetMimetype(), "video/mp4"), nil
	case m.AudioMessage != nil:
		return m.AudioMessage, firstNonEmpty(m.AudioMessage.GetMimetype(), "audio/ogg"), nil
	case m.DocumentMessage != nil:
		return m.DocumentMessage, firstNonEmpty(m.DocumentMessage.GetMimetype(), "application/octet-stream"), nil
	case m.StickerMessage != nil:
		return m.StickerMessage, firstNonEmpty(m.StickerMessage.GetMimetype(), "image/webp"), nil
	}
	return nil, "", errors.New("invalid media reference: no attachment")
}
