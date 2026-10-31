package whatsappservice

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
)

const (
	approvalVersion     = "v1"
	approvalMaxValidity = 5 * time.Minute
)

func ApprovalRecipient(to string) (string, error) {
	to = strings.TrimSpace(to)
	if strings.Contains(to, "@") {
		jid, err := whatsappprovider.ParseChatJID(to)
		if err != nil {
			return "", err
		}
		return jid.String(), nil
	}
	digits, err := whatsappprovider.NormalizePhone(to)
	if err != nil {
		return "", err
	}
	return digits + "@s.whatsapp.net", nil
}

func TextDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func FileDigest(data []byte, fileName, caption string) string {
	sum := sha256.Sum256(data)
	return TextDigest("file\n" + hex.EncodeToString(sum[:]) + "\n" + fileName + "\n" + caption)
}

func MintApproval(secret []byte, recipient, digest string, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	return approvalVersion + "." + exp + "." + approvalMAC(secret, recipient, digest, exp)
}

func approvalMAC(secret []byte, recipient, digest, exp string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("meowmcp-send-" + approvalVersion + "\n" + recipient + "\n" + digest + "\n" + exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type approvals struct {
	secret []byte
	now    func() time.Time

	mu   sync.Mutex
	used map[string]time.Time
}

func newApprovals(secret string) *approvals {
	if secret == "" {
		return nil
	}
	return &approvals{secret: []byte(secret), now: time.Now, used: make(map[string]time.Time)}
}

func (a *approvals) reserve(to, digest, token string) (finish func(sent bool), err error) {
	if a == nil {
		return func(bool) {}, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, domain.ErrApprovalRequired
	}
	recipient, err := ApprovalRecipient(to)
	if err != nil {
		return nil, err
	}
	version, rest, _ := strings.Cut(token, ".")
	exp, mac, _ := strings.Cut(rest, ".")
	expUnix, perr := strconv.ParseInt(exp, 10, 64)
	if version != approvalVersion || perr != nil || mac == "" {
		return nil, fmt.Errorf("%w: malformed token", domain.ErrApprovalInvalid)
	}
	now := a.now()
	expires := time.Unix(expUnix, 0)
	switch {
	case !expires.After(now):
		return nil, fmt.Errorf("%w: expired", domain.ErrApprovalInvalid)
	case expires.After(now.Add(approvalMaxValidity)):
		return nil, fmt.Errorf("%w: valid for longer than %s", domain.ErrApprovalInvalid, approvalMaxValidity)
	case !hmac.Equal([]byte(mac), []byte(approvalMAC(a.secret, recipient, digest, exp))):
		return nil, fmt.Errorf("%w: it was approved for a different recipient or content", domain.ErrApprovalInvalid)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for k, until := range a.used {
		if !until.After(now) {
			delete(a.used, k)
		}
	}
	if _, ok := a.used[mac]; ok {
		return nil, fmt.Errorf("%w: already used", domain.ErrApprovalInvalid)
	}
	a.used[mac] = expires
	return func(sent bool) {
		if sent {
			return
		}
		a.mu.Lock()
		delete(a.used, mac)
		a.mu.Unlock()
	}, nil
}
