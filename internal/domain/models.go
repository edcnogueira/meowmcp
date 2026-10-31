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
	Seq               int64
	ID                string
	ChatJID           string
	SenderJID         string
	SenderName        string
	AgentName         string
	FromMe            bool
	Timestamp         time.Time
	Text              string
	MediaType         string
	ViewOnce          bool
	QuotedID          string
	Mentions          []Mention
	Transcript        string
	TranscriptUnclear bool
	MediaRef          []byte
	HasMediaKey       bool
	MediaFile         string
	MediaStatus       MediaStatus
}

type Mention struct {
	JID   string
	Phone string
	Name  string
	IsMe  bool
}

type MediaStatus string

const (
	MediaStatusReady        MediaStatus = "ready"
	MediaStatusDownloadable MediaStatus = "downloadable"
	MediaStatusMissingKey   MediaStatus = "missing_key"
)

func (s MediaStatus) String() string { return string(s) }

func IsDownloadableMediaType(mediaType string) bool {
	switch mediaType {
	case "image", "video", "audio", "voice", "document", "sticker":
		return true
	}
	return false
}

type Transcript struct {
	Text    string
	Unclear bool
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
	Text      string
	MediaType string
	MediaRef  []byte
}

type MediaKind string

const (
	MediaKindImage    MediaKind = "image"
	MediaKindVideo    MediaKind = "video"
	MediaKindAudio    MediaKind = "audio"
	MediaKindVoice    MediaKind = "voice"
	MediaKindDocument MediaKind = "document"
)

func (k MediaKind) String() string { return string(k) }

type OutgoingMedia struct {
	Kind     MediaKind
	MimeType string
	FileName string
	Caption  string
	Data     []byte
}
