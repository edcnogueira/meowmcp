package mcptransport

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type sendMessageInput struct {
	To        string `json:"to" jsonschema:"recipient: phone number with country code (e.g. +5511999998888) or chat JID (user or group). A phone number starts a new conversation if none exists"`
	Text      string `json:"text" jsonschema:"message text, written as the user (first person). WhatsApp formatting: *bold* _italic_ ~strike~"`
	ReplyToID string `json:"reply_to_id,omitempty" jsonschema:"ID of a message in the same chat to quote-reply"`
	Approval  string `json:"approval,omitempty" jsonschema:"approval token issued by the user's client for this exact recipient and text; required when get_status reports send_approval required. Never invent one"`
}

type sendMessageOutput struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid" jsonschema:"use this chat with get_chat_state, get_messages and event subscriptions"`
	ChatName  string `json:"chat_name,omitempty"`
	Timestamp string `json:"timestamp"`
	Seq       int64  `json:"seq" jsonschema:"position of the sent message; replies arrive with a higher seq"`
	Text      string `json:"text" jsonschema:"exactly what was sent"`
}

type sendFileInput struct {
	To         string `json:"to" jsonschema:"recipient: phone number with country code (e.g. +5511999998888) or chat JID (user or group). A phone number starts a new conversation if none exists"`
	FilePath   string `json:"file_path,omitempty" jsonschema:"absolute path of the file on the server host (e.g. a get_media file_path). Use this or data_base64"`
	DataBase64 string `json:"data_base64,omitempty" jsonschema:"file content encoded as standard base64, for files not on the server host. Requires file_name"`
	FileName   string `json:"file_name,omitempty" jsonschema:"file name shown to the recipient for documents; its extension sets the file type. Default: base name of file_path"`
	Caption    string `json:"caption,omitempty" jsonschema:"text sent with an image, video or document, written as the user. Not supported for audio and voice"`
	SendAs     string `json:"send_as,omitempty" jsonschema:"image, video, audio, voice or document. Default: image for JPEG/PNG, video for MP4/3GP, audio for audio files, document otherwise. document sends any file uncompressed; voice needs an OGG/Opus file and shows as a voice note"`
	ReplyToID  string `json:"reply_to_id,omitempty" jsonschema:"ID of a message in the same chat to quote-reply"`
	Approval   string `json:"approval,omitempty" jsonschema:"approval token issued by the user's client for this exact recipient, file and caption; required when get_status reports send_approval required. Never invent one"`
}

type sendFileOutput struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid" jsonschema:"use this chat with get_chat_state, get_messages and event subscriptions"`
	ChatName  string `json:"chat_name,omitempty"`
	Timestamp string `json:"timestamp"`
	Seq       int64  `json:"seq" jsonschema:"position of the sent message; replies arrive with a higher seq"`
	MediaType string `json:"media_type" jsonschema:"how the file was sent: image, video, audio, voice or document"`
	MimeType  string `json:"mime_type"`
	FileName  string `json:"file_name" jsonschema:"file name that was sent"`
	Caption   string `json:"caption,omitempty" jsonschema:"exactly the caption that was sent"`
	SizeBytes int64  `json:"size_bytes"`
}

type waitForReplyInput struct {
	Chat           string `json:"chat,omitempty" jsonschema:"chat to wait on (JID or phone number). Omit to wait for a message from any chat"`
	SinceID        int64  `json:"since_id,omitempty" jsonschema:"return incoming messages stored after this seq; ones already there return immediately. Default: your last sent message in chat (or now). Pass next_since_id from a previous call to only get newer messages"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"how long to block (default 60, max 240)"`
}

type waitForReplyOutput struct {
	Messages    []messageOutput `json:"messages" jsonschema:"incoming messages in arrival order"`
	TimedOut    bool            `json:"timed_out" jsonschema:"true when nothing arrived before the timeout"`
	SinceID     int64           `json:"since_id"`
	NextSinceID int64           `json:"next_since_id" jsonschema:"pass as since_id to wait for messages newer than these"`
	MarkedRead  bool            `json:"marked_read" jsonschema:"always false: waiting never sends read receipts"`
}

type markAsReadInput struct {
	Chat string `json:"chat" jsonschema:"chat JID or phone number"`
}

type markAsReadOutput struct {
	Marked int `json:"marked" jsonschema:"number of incoming messages acknowledged"`
}

func (t *Transport) registerWriteTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "send_message",
		Description: "Send a WhatsApp text message from the user's account. Works for existing chats and for starting a new conversation with any phone number. " +
			"The message appears as written by the user: confirm recipient and text with them first. When the server requires approval, it refuses to send without " +
			"an approval token the user's client issued for this exact recipient and text. The result echoes the text that went out.",
		Annotations: &mcp.ToolAnnotations{Title: "Send WhatsApp message", DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, t.sendMessage)

	mcp.AddTool(server, &mcp.Tool{
		Name: "send_file",
		Description: "Send a file from the user's account: photo, video, audio, voice note or any document (PDF, spreadsheet, etc.), with an optional caption. " +
			"Takes a file on the server host (file_path) or its content as base64. Same recipients as send_message, including new conversations. " +
			"The file goes out under the user's name: confirm recipient, file and caption with them first. When the server requires approval, it refuses to send " +
			"without an approval token the user's client issued for this exact recipient, file and caption.",
		Annotations: &mcp.ToolAnnotations{Title: "Send WhatsApp file", DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, t.sendFile)

	mcp.AddTool(server, &mcp.Tool{
		Name: "wait_for_reply",
		Description: "Block up to timeout_seconds for new incoming WhatsApp messages in a chat (or any chat). Only for answers expected within seconds, " +
			"such as bots or verification codes: for people, subscribe to the chat.message_received event instead of holding the turn. " +
			"Messages sent in quick succession are returned together.",
		Annotations: &mcp.ToolAnnotations{Title: "Wait for WhatsApp reply", ReadOnlyHint: true, OpenWorldHint: new(false)},
	}, t.waitForReply)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "mark_as_read",
		Description: "Send read receipts (blue ticks) for the latest incoming messages of a chat. Only when the user wants the other side to see the messages were read.",
		Annotations: &mcp.ToolAnnotations{Title: "Mark chat as read", IdempotentHint: true, DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, t.markAsRead)
}

func (t *Transport) sendMessage(ctx context.Context, _ *mcp.CallToolRequest, in sendMessageInput) (*mcp.CallToolResult, sendMessageOutput, error) {
	resp, err := t.service.SendMessage(ctx, &whatsappservice.SendMessageRequest{To: in.To, Text: in.Text, ReplyToID: in.ReplyToID, Approval: in.Approval})
	if err != nil {
		return nil, sendMessageOutput{}, err
	}
	return nil, sendMessageOutput{
		MessageID: resp.Sent.ID,
		ChatJID:   resp.Sent.ChatJID,
		ChatName:  resp.Chat.Name,
		Timestamp: formatTime(resp.Sent.Timestamp),
		Seq:       resp.Seq,
		Text:      resp.Sent.Text,
	}, nil
}

func (t *Transport) sendFile(ctx context.Context, _ *mcp.CallToolRequest, in sendFileInput) (*mcp.CallToolResult, sendFileOutput, error) {
	var data []byte
	if encoded := strings.Join(strings.Fields(in.DataBase64), ""); encoded != "" {
		var err error
		if data, err = base64.StdEncoding.DecodeString(encoded); err != nil {
			return nil, sendFileOutput{}, &inputError{field: "data_base64", msg: "is not valid standard base64: " + err.Error()}
		}
	}
	resp, err := t.service.SendFile(ctx, &whatsappservice.SendFileRequest{
		To:        in.To,
		Path:      in.FilePath,
		Data:      data,
		FileName:  in.FileName,
		Caption:   in.Caption,
		SendAs:    in.SendAs,
		ReplyToID: in.ReplyToID,
		Approval:  in.Approval,
	})
	if err != nil {
		return nil, sendFileOutput{}, err
	}
	return nil, sendFileOutput{
		MessageID: resp.Sent.ID,
		ChatJID:   resp.Sent.ChatJID,
		ChatName:  resp.Chat.Name,
		Timestamp: formatTime(resp.Sent.Timestamp),
		Seq:       resp.Seq,
		MediaType: resp.Sent.MediaType,
		MimeType:  resp.MimeType,
		FileName:  resp.FileName,
		Caption:   resp.Caption,
		SizeBytes: resp.Size,
	}, nil
}

func (t *Transport) waitForReply(ctx context.Context, _ *mcp.CallToolRequest, in waitForReplyInput) (*mcp.CallToolResult, waitForReplyOutput, error) {
	resp, err := t.service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{
		Chat:    in.Chat,
		SinceID: in.SinceID,
		Timeout: time.Duration(in.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, waitForReplyOutput{}, err
	}
	return nil, waitForReplyOutput{
		Messages:    toMessageOutputs(resp.Messages),
		TimedOut:    resp.TimedOut,
		SinceID:     resp.SinceID,
		NextSinceID: resp.NextSinceID,
	}, nil
}

func (t *Transport) markAsRead(ctx context.Context, _ *mcp.CallToolRequest, in markAsReadInput) (*mcp.CallToolResult, markAsReadOutput, error) {
	n, err := t.service.MarkRead(ctx, in.Chat)
	if err != nil {
		return nil, markAsReadOutput{}, err
	}
	return nil, markAsReadOutput{Marked: n}, nil
}
