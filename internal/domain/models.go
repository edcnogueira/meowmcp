package domain

import "time"

type ConnectionState string

const (
	ConnectionStateLoggedOut    ConnectionState = "logged_out"
	ConnectionStatePairing      ConnectionState = "pairing"
	ConnectionStateConnecting   ConnectionState = "connecting"
	ConnectionStateConnected    ConnectionState = "connected"
	ConnectionStateDisconnected ConnectionState = "disconnected"
)

func (s ConnectionState) String() string { return string(s) }

type Status struct {
	State     ConnectionState
	JID       string
	PushName  string
	Pairing   *Pairing
	LastError string
}

type Pairing struct {
	QRCode       string
	QRExpiresAt  time.Time
	PairingCode  string
	PairingPhone string
}

type Chat struct {
	JID           string
	Name          string
	IsGroup       bool
	LastMessageAt time.Time
	LastMessage   *Message
}

type Message struct {
	ID         string
	ChatJID    string
	SenderJID  string
	SenderName string
	FromMe     bool
	Timestamp  time.Time
	Text       string
	MediaType  string
	QuotedID   string
	Transcript string
	MediaRef   []byte
}

type Contact struct {
	JID          string
	Phone        string
	Name         string
	PushName     string
	BusinessName string
}

type NumberCheck struct {
	Query        string
	JID          string
	OnWhatsApp   bool
	BusinessName string
}

type SentMessage struct {
	ID        string
	ChatJID   string
	Timestamp time.Time
}
