package whatsappprovider

import (
	"strings"

	"go.mau.fi/whatsmeow/types"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

func NormalizePhone(s string) (string, error) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return "", domain.ErrInvalidRecipient
		}
	}
	digits := strings.TrimPrefix(b.String(), "00")
	if len(digits) < 8 || len(digits) > 15 {
		return "", domain.ErrInvalidRecipient
	}
	return digits, nil
}

func ParseChatJID(s string) (types.JID, error) {
	jid, err := types.ParseJID(strings.TrimSpace(s))
	if err != nil || jid.User == "" {
		return types.JID{}, domain.ErrInvalidRecipient
	}
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
		return jid.ToNonAD(), nil
	}
	return types.JID{}, domain.ErrInvalidRecipient
}
