package mcptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

const maxInlineImageBytes = 10 << 20

type getMediaInput struct {
	Chat       string `json:"chat" jsonschema:"chat JID or phone number of the conversation the message is in"`
	MessageID  string `json:"message_id" jsonschema:"id of a message whose media_type is image, sticker, voice, audio, video or document"`
	Transcribe bool   `json:"transcribe,omitempty" jsonschema:"voice/audio only: send the audio to the server's speech-to-text API and return the text. Default false: the audio is only downloaded. Set it only when the spoken content is needed"`
}

type mediaOutput struct {
	MessageID          string `json:"message_id"`
	ChatJID            string `json:"chat_jid"`
	MediaType          string `json:"media_type"`
	MimeType           string `json:"mime_type"`
	ViewOnce           bool   `json:"view_once,omitempty" jsonschema:"view-once media, returned from the copy stored when it arrived"`
	FilePath           string `json:"file_path" jsonschema:"local copy of the attachment on the server host"`
	SizeBytes          int64  `json:"size_bytes"`
	Caption            string `json:"caption,omitempty" jsonschema:"caption or file name sent with the media"`
	Transcript         string `json:"transcript,omitempty" jsonschema:"speech-to-text of voice/audio"`
	TranscriptUnclear  bool   `json:"transcript_unclear,omitempty" jsonschema:"speech-to-text had low confidence: treat the transcript as unreliable and do not complete or guess words"`
	TranscriptionError string `json:"transcription_error,omitempty" jsonschema:"why audio has no transcript"`
	MarkedRead         bool   `json:"marked_read" jsonschema:"always false: fetching media never sends read receipts"`
	Note               string `json:"note,omitempty"`
}

func (t *Transport) getMedia(ctx context.Context, _ *mcp.CallToolRequest, in getMediaInput) (*mcp.CallToolResult, mediaOutput, error) {
	resp, err := t.service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: in.Chat, MessageID: in.MessageID, Transcribe: in.Transcribe})
	if err != nil {
		return nil, mediaOutput{}, err
	}
	out := mediaOutput{
		MessageID:          resp.Message.ID,
		ChatJID:            resp.Message.ChatJID,
		MediaType:          resp.Message.MediaType,
		MimeType:           resp.MimeType,
		ViewOnce:           resp.Message.ViewOnce,
		FilePath:           resp.Path,
		SizeBytes:          resp.Size,
		Caption:            resp.Message.Text,
		Transcript:         resp.Transcript,
		TranscriptUnclear:  resp.TranscriptUnclear,
		TranscriptionError: resp.TranscriptionError,
	}

	var image []byte
	switch {
	case strings.HasPrefix(resp.MimeType, "image/") && resp.Size <= maxInlineImageBytes:
		if image, err = os.ReadFile(resp.Path); err != nil {
			return nil, mediaOutput{}, fmt.Errorf("failed to read image: %w", err)
		}
	case strings.HasPrefix(resp.MimeType, "image/"):
		out.Note = fmt.Sprintf("image larger than %d MB is not attached; open it from file_path", maxInlineImageBytes>>20)
	case strings.HasPrefix(resp.MimeType, "audio/") && resp.Transcript == "" && resp.TranscriptionError == "":
		out.Note = "audio not transcribed; call get_media again with transcribe=true to get the text"
	case !strings.HasPrefix(resp.MimeType, "audio/"):
		out.Note = "content is not rendered for this type; open it from file_path"
	}

	summary, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, mediaOutput{}, fmt.Errorf("failed to encode media result: %w", err)
	}
	content := []mcp.Content{&mcp.TextContent{Text: string(summary)}}
	if image != nil {
		content = append(content, &mcp.ImageContent{Data: image, MIMEType: resp.MimeType})
	}
	return &mcp.CallToolResult{Content: content}, out, nil
}
