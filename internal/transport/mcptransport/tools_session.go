package mcptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mdp/qrterminal/v3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/skip2/go-qrcode"

	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

const qrImageSize = 512

type emptyInput struct{}

type loginInput struct {
	Phone   string `json:"phone,omitempty" jsonschema:"optional phone number of the WhatsApp account with country code (e.g. +5511999998888); when set, an 8-character pairing code is also returned for users who cannot scan a QR code"`
	ASCIIQR bool   `json:"ascii_qr,omitempty" jsonschema:"also return the QR code drawn with text characters, for chat surfaces that cannot display images"`
}

type logoutOutput struct {
	LoggedOut bool `json:"logged_out"`
}

func (t *Transport) registerSessionTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_status",
		Description: "Get the WhatsApp session state (logged_out, pairing, connecting, connected, disconnected), the logged-in account and, while pairing, the current QR/pairing code.",
		Annotations: &mcp.ToolAnnotations{Title: "WhatsApp status", ReadOnlyHint: true, OpenWorldHint: new(false)},
	}, t.getStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name: "login",
		Description: "Link this server to the user's WhatsApp account as a companion device. Returns a QR code (PNG image, file path, raw payload, optional ASCII art) " +
			"for the user to scan in WhatsApp > Linked devices > Link a device. Pass phone to also get an 8-character pairing code for 'Link with phone number instead'. " +
			"QR codes rotate about every 20-60 seconds: call login again to get the current one; poll get_status until connected. Safe to call repeatedly; no-op when already logged in.",
		Annotations: &mcp.ToolAnnotations{Title: "Log in to WhatsApp", IdempotentHint: true, DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, t.login)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "logout",
		Description: "Unlink this server from the user's WhatsApp account. A new QR scan is required afterwards. Only call when the user explicitly asks.",
		Annotations: &mcp.ToolAnnotations{Title: "Log out of WhatsApp", DestructiveHint: new(true), OpenWorldHint: new(true)},
	}, t.logout)
}

func (t *Transport) getStatus(_ context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, statusOutput, error) {
	st := t.service.Status()
	path := ""
	if st.Pairing != nil && st.Pairing.QRCode != "" {
		if _, p, err := t.renderQR(st.Pairing.QRCode); err == nil {
			path = p
		}
	}
	return nil, toStatusOutput(st, path, t.service.SendApprovalRequired()), nil
}

func (t *Transport) login(ctx context.Context, _ *mcp.CallToolRequest, in loginInput) (*mcp.CallToolResult, statusOutput, error) {
	st, err := t.service.Login(ctx, &whatsappservice.LoginRequest{Phone: in.Phone})
	if err != nil {
		return nil, statusOutput{}, err
	}
	if st.Pairing == nil || st.Pairing.QRCode == "" {
		return nil, toStatusOutput(st, "", t.service.SendApprovalRequired()), nil
	}

	png, path, err := t.renderQR(st.Pairing.QRCode)
	if err != nil {
		return nil, statusOutput{}, err
	}
	out := toStatusOutput(st, path, t.service.SendApprovalRequired())

	summary, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, statusOutput{}, fmt.Errorf("failed to encode login result: %w", err)
	}
	content := []mcp.Content{
		&mcp.TextContent{Text: string(summary)},
		&mcp.ImageContent{Data: png, MIMEType: "image/png"},
	}
	if in.ASCIIQR {
		var buf bytes.Buffer
		qrterminal.GenerateHalfBlock(st.Pairing.QRCode, qrterminal.L, &buf)
		content = append(content, &mcp.TextContent{Text: "Scan this QR code in WhatsApp > Linked devices > Link a device (use a monospace font):\n" + buf.String()})
	}
	return &mcp.CallToolResult{Content: content}, out, nil
}

func (t *Transport) logout(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, logoutOutput, error) {
	if err := t.service.Logout(ctx); err != nil {
		return nil, logoutOutput{}, err
	}
	_ = os.Remove(t.qrImagePath)
	return nil, logoutOutput{LoggedOut: true}, nil
}

func (t *Transport) renderQR(code string) (png []byte, path string, err error) {
	png, err = qrcode.Encode(code, qrcode.Medium, qrImageSize)
	if err != nil {
		return nil, "", fmt.Errorf("failed to render qr code: %w", err)
	}
	if err := os.WriteFile(t.qrImagePath, png, 0o600); err == nil {
		path = t.qrImagePath
	}
	return png, path, nil
}
