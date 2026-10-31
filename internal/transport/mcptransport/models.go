package mcptransport

import (
	"strings"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

type statusOutput struct {
	State        string         `json:"state" jsonschema:"logged_out, pairing, connecting, connected or disconnected"`
	JID          string         `json:"jid,omitempty" jsonschema:"the logged-in account JID"`
	PushName     string         `json:"push_name,omitempty" jsonschema:"the logged-in account display name"`
	LastError    string         `json:"last_error,omitempty"`
	Pairing      *pairingOutput `json:"pairing,omitempty" jsonschema:"present while state is pairing"`
	SendApproval string         `json:"send_approval" jsonschema:"required: send_message and send_file refuse to send without an approval token issued by the user's client; off: no token needed"`
	NextStep     string         `json:"next_step" jsonschema:"what to do next"`
}

type pairingOutput struct {
	QRCode       string `json:"qr_code" jsonschema:"raw QR payload; render as a QR code to scan"`
	QRImagePath  string `json:"qr_image_path,omitempty" jsonschema:"local PNG file of the current QR code"`
	QRExpiresAt  string `json:"qr_expires_at" jsonschema:"when this QR code rotates; call login again for the next one"`
	PairingCode  string `json:"pairing_code,omitempty" jsonschema:"8-character code for Link with phone number"`
	PairingPhone string `json:"pairing_phone,omitempty"`
}

type chatOutput struct {
	JID           string         `json:"jid"`
	Name          string         `json:"name,omitempty"`
	Phone         string         `json:"phone,omitempty"`
	IsGroup       bool           `json:"is_group"`
	LastMessageAt string         `json:"last_message_at,omitempty"`
	LastMessage   *messageOutput `json:"last_message,omitempty"`
}

type messageOutput struct {
	Seq               int64           `json:"seq" jsonschema:"local arrival order, strictly increasing; pass the highest one seen as since_id to read only newer messages"`
	ID                string          `json:"id"`
	ChatJID           string          `json:"chat_jid"`
	SenderJID         string          `json:"sender_jid,omitempty"`
	SenderName        string          `json:"sender_name,omitempty"`
	AgentName         string          `json:"agent_name,omitempty" jsonschema:"attendant who wrote this message for a business account; the *Name:* signature is removed from text"`
	FromMe            bool            `json:"from_me" jsonschema:"true when sent by the logged-in user"`
	Timestamp         string          `json:"timestamp"`
	Text              string          `json:"text,omitempty"`
	Mentions          []mentionOutput `json:"mentions,omitempty" jsonschema:"who each @tag in text refers to"`
	MediaType         string          `json:"media_type,omitempty" jsonschema:"set for non-text messages: image, video, audio, voice, document, sticker, location, contact, poll, buttons, list, interactive. For image, sticker, voice, audio, video and document call get_media to see or transcribe the content"`
	MediaStatus       string          `json:"media_status,omitempty" jsonschema:"attachments only: ready (stored on the server, get_media returns it), downloadable (get_media fetches it from WhatsApp) or missing_key (no download key in history; cannot be fetched, ask the sender to resend)"`
	ViewOnce          bool            `json:"view_once,omitempty" jsonschema:"view-once photo, video or audio; stored on arrival so get_media returns it"`
	Transcript        string          `json:"transcript,omitempty" jsonschema:"speech-to-text of a voice/audio message, present once get_media has transcribed it"`
	TranscriptUnclear bool            `json:"transcript_unclear,omitempty" jsonschema:"speech-to-text had low confidence: treat the transcript as unreliable and do not complete or guess words"`
	ReplyToID         string          `json:"reply_to_id,omitempty" jsonschema:"ID of the quoted message"`
}

type mentionOutput struct {
	Tag   string `json:"tag" jsonschema:"the @tag as it appears in text"`
	JID   string `json:"jid"`
	Phone string `json:"phone,omitempty"`
	Name  string `json:"name,omitempty"`
	IsMe  bool   `json:"is_me,omitempty" jsonschema:"the tag is the logged-in user (you)"`
}

type contactOutput struct {
	JID          string `json:"jid"`
	Phone        string `json:"phone,omitempty"`
	Name         string `json:"name,omitempty"`
	PushName     string `json:"push_name,omitempty"`
	BusinessName string `json:"business_name,omitempty"`
}

type numberCheckOutput struct {
	Query        string `json:"query"`
	OnWhatsApp   bool   `json:"on_whatsapp"`
	JID          string `json:"jid,omitempty" jsonschema:"chat JID to use with send_message and get_messages"`
	BusinessName string `json:"business_name,omitempty"`
}

func toStatusOutput(st domain.Status, qrImagePath string, approvalRequired bool) statusOutput {
	out := statusOutput{
		State:        st.State.String(),
		JID:          st.JID,
		PushName:     st.PushName,
		LastError:    st.LastError,
		SendApproval: "off",
	}
	if approvalRequired {
		out.SendApproval = "required"
	}
	if st.Pairing != nil {
		out.Pairing = &pairingOutput{
			QRCode:       st.Pairing.QRCode,
			QRImagePath:  qrImagePath,
			QRExpiresAt:  formatTime(st.Pairing.QRExpiresAt),
			PairingCode:  st.Pairing.PairingCode,
			PairingPhone: st.Pairing.PairingPhone,
		}
	}
	switch st.State {
	case domain.ConnectionStateLoggedOut:
		out.NextStep = "Call login to get a QR code (or login with phone for a pairing code) and show it to the user."
	case domain.ConnectionStatePairing:
		if out.Pairing != nil && out.Pairing.PairingCode != "" {
			out.NextStep = "Ask the user to open WhatsApp > Linked devices > Link a device > Link with phone number instead, and type the pairing_code. Then poll get_status until connected."
		} else {
			out.NextStep = "Ask the user to scan the QR code in WhatsApp > Linked devices > Link a device. Codes rotate; call login again for a fresh one. Poll get_status until connected."
		}
	case domain.ConnectionStateConnecting:
		out.NextStep = "Paired; the connection is being established. Retry get_status in a few seconds."
	case domain.ConnectionStateConnected:
		out.NextStep = "Ready."
	case domain.ConnectionStateDisconnected:
		out.NextStep = "Offline and not retrying; see last_error. Restarting the server may be required."
	}
	return out
}

func toChatOutput(c domain.Chat) chatOutput {
	out := chatOutput{
		JID:           c.JID,
		Name:          c.Name,
		Phone:         phoneOf(c.JID),
		IsGroup:       c.IsGroup,
		LastMessageAt: formatTime(c.LastMessageAt),
	}
	if c.LastMessage != nil {
		m := toMessageOutput(*c.LastMessage)
		out.LastMessage = &m
	}
	return out
}

func toMessageOutput(m domain.Message) messageOutput {
	out := messageOutput{
		Seq:               m.Seq,
		ID:                m.ID,
		ChatJID:           m.ChatJID,
		SenderJID:         m.SenderJID,
		SenderName:        m.SenderName,
		AgentName:         m.AgentName,
		FromMe:            m.FromMe,
		Timestamp:         formatTime(m.Timestamp),
		Text:              m.Text,
		MediaType:         m.MediaType,
		MediaStatus:       m.MediaStatus.String(),
		ViewOnce:          m.ViewOnce,
		ReplyToID:         m.QuotedID,
		Transcript:        m.Transcript,
		TranscriptUnclear: m.TranscriptUnclear,
	}
	for _, mention := range m.Mentions {
		user, _, _ := strings.Cut(mention.JID, "@")
		out.Mentions = append(out.Mentions, mentionOutput{
			Tag:   "@" + user,
			JID:   mention.JID,
			Phone: mention.Phone,
			Name:  mention.Name,
			IsMe:  mention.IsMe,
		})
	}
	return out
}

func toOptionalMessage(m *domain.Message) *messageOutput {
	if m == nil {
		return nil
	}
	out := toMessageOutput(*m)
	return &out
}

func toMessageOutputs(msgs []domain.Message) []messageOutput {
	out := make([]messageOutput, len(msgs))
	for i, m := range msgs {
		out[i] = toMessageOutput(m)
	}
	return out
}

func phoneOf(jid string) string {
	if user, server, ok := strings.Cut(jid, "@"); ok && server == "s.whatsapp.net" {
		return "+" + user
	}
	return ""
}
