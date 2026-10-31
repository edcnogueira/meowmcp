package webhookprovider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	SecretPrefix   = "whsec_"
	requestTimeout = 5 * time.Second
	maxBodyRead    = 64 << 10
)

const (
	ReasonConnectionRefused = "connection_refused"
	ReasonTimeout           = "timeout"
	ReasonTLSError          = "tls_error"
	ReasonHTTP4xx           = "http_4xx"
	ReasonHTTP5xx           = "http_5xx"
	ReasonChallengeFailed   = "challenge_failed"
)

var errBlockedAddress = errors.New("callback address is not publicly routable")

type DeliveryError struct {
	Reason    string
	Retryable bool
	Err       error
}

func (e *DeliveryError) Error() string { return e.Reason + ": " + e.Err.Error() }

func (e *DeliveryError) Unwrap() error { return e.Err }

type Config struct {
	AllowPrivate bool
	TLSConfig    *tls.Config
}

type Provider struct {
	http *http.Client
}

func New(cfg Config) *Provider {
	dialer := &net.Dialer{Timeout: requestTimeout}
	if !cfg.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || !isPublic(addr.Unmap()) {
				return errBlockedAddress
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSClientConfig:     cfg.TLSConfig,
		TLSHandshakeTimeout: requestTimeout,
		MaxIdleConns:        8,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Provider{http: &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type Delivery struct {
	URL            string
	Secret         []byte
	SubscriptionID string
	MessageID      string
	Body           []byte
}

func (p *Provider) Post(ctx context.Context, d Delivery) ([]byte, error) {
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(d.Body))
	if err != nil {
		return nil, &DeliveryError{Reason: ReasonConnectionRefused, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", d.MessageID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("webhook-signature", Sign(d.Secret, d.MessageID, ts, d.Body))
	req.Header.Set("X-MCP-Subscription-Id", d.SubscriptionID)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, &DeliveryError{Reason: transportReason(err), Retryable: true, Err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, nil
	case resp.StatusCode >= 500:
		return nil, &DeliveryError{Reason: ReasonHTTP5xx, Retryable: true, Err: fmt.Errorf("endpoint answered %d", resp.StatusCode)}
	}
	return nil, &DeliveryError{
		Reason:    ReasonHTTP4xx,
		Retryable: resp.StatusCode != http.StatusRequestEntityTooLarge,
		Err:       fmt.Errorf("endpoint answered %d", resp.StatusCode),
	}
}

func Sign(secret []byte, id string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id + "." + strconv.FormatInt(ts, 10) + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func ParseSecret(s string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(s, SecretPrefix)
	if !ok {
		return nil, fmt.Errorf("secret must start with %q", SecretPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secret is not valid base64: %w", err)
	}
	if len(key) < 24 || len(key) > 64 {
		return nil, fmt.Errorf("secret must decode to 24-64 bytes, got %d", len(key))
	}
	return key, nil
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("invalid callback url: %w", err)
	case u.Scheme != "https":
		return errors.New("callback url must use https")
	case u.Hostname() == "":
		return errors.New("callback url has no host")
	case u.User != nil:
		return errors.New("callback url must not carry credentials")
	}
	return nil
}

func transportReason(err error) string {
	var (
		netErr  net.Error
		certErr *tls.CertificateVerificationError
		recErr  tls.RecordHeaderError
	)
	switch {
	case errors.As(err, &certErr), errors.As(err, &recErr), strings.Contains(err.Error(), "tls:"):
		return ReasonTLSError
	case errors.As(err, &netErr) && netErr.Timeout():
		return ReasonTimeout
	}
	return ReasonConnectionRefused
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func isPublic(addr netip.Addr) bool {
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}
