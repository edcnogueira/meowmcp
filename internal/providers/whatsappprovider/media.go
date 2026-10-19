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
