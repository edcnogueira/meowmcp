package mcptransport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jamillosantos/logctx"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/webhookprovider"
)

const (
	eventChatMessage       = "chat.message_received"
	defaultSubscriptionTTL = 24 * time.Hour
	minSubscriptionTTL     = time.Minute
	verificationTTL        = 24 * time.Hour
	eventQuietWindow       = 4 * time.Second
	eventMaxGathering      = 30 * time.Second
	maxEventMessages       = 50
	maxReplayAge           = 24 * time.Hour
	timeLayout             = "2006-01-02T15:04:05+00:00"
)

const (
	codeNotFound         = -32023
	codeForbidden        = -32024
	codeUnsupported      = -32026
	codeCallbackEndpoint = -32027
)

var deliveryBackoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

type WebhookSender interface {
	Post(ctx context.Context, d webhookprovider.Delivery) ([]byte, error)
}

type watchChatArgs struct {
	Chat string `json:"chat" jsonschema:"chat to watch: JID or phone number with country code"`
}

type eventChat struct {
	JID     string `json:"jid"`
	Name    string `json:"name,omitempty"`
	Phone   string `json:"phone,omitempty"`
	IsGroup bool   `json:"is_group"`
}

type chatMessageEvent struct {
	Chat       eventChat       `json:"chat"`
	Messages   []messageOutput `json:"messages" jsonschema:"incoming messages that arrived together, in arrival order"`
	SinceID    int64           `json:"since_id" jsonschema:"seq of the last message here; get_messages with this since_id returns only newer ones"`
	HasMore    bool            `json:"has_more" jsonschema:"more messages follow in another event"`
	MarkedRead bool            `json:"marked_read" jsonschema:"always false: delivering an event never sends read receipts"`
}

type eventOccurrence struct {
	EventID   string           `json:"eventId"`
	Name      string           `json:"name"`
	Timestamp string           `json:"timestamp"`
	Data      chatMessageEvent `json:"data"`
	Cursor    string           `json:"cursor"`
}

type eventType struct {
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Delivery      []string           `json:"delivery"`
	InputSchema   *jsonschema.Schema `json:"inputSchema"`
	PayloadSchema *jsonschema.Schema `json:"payloadSchema"`
}

type eventsListParams struct {
	mcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type eventsListResult struct {
	mcp.ResultBase
	Events []eventType `json:"events"`
}

type eventDelivery struct {
	Mode   string `json:"mode,omitempty"`
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

type eventsSubscribeParams struct {
	mcp.ParamsBase
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Delivery  eventDelivery  `json:"delivery"`
	Cursor    *string        `json:"cursor,omitempty"`
	MaxAgeMs  *int64         `json:"maxAgeMs,omitempty"`
	TTLMs     *int64         `json:"ttlMs,omitempty"`
}

type deliveryStatus struct {
	Active         bool    `json:"active"`
	LastDeliveryAt *string `json:"lastDeliveryAt"`
	LastError      *string `json:"lastError"`
	FailedSince    *string `json:"failedSince,omitempty"`
}

type eventsSubscribeResult struct {
	mcp.ResultBase
	ID             string          `json:"id"`
	RefreshBefore  string          `json:"refreshBefore"`
	Cursor         string          `json:"cursor"`
	Truncated      bool            `json:"truncated"`
	DeliveryStatus *deliveryStatus `json:"deliveryStatus,omitempty"`
}

type eventsUnsubscribeParams struct {
	mcp.ParamsBase
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Delivery  eventDelivery  `json:"delivery"`
}

type eventsUnsubscribeResult struct {
	mcp.ResultBase
}

type eventSubscription struct {
	id        string
	url       string
	chat      domain.Chat
	aliases   []string
	notBefore time.Time
	wake      chan struct{}
	cancel    context.CancelFunc

	secret         []byte
	expiresAt      time.Time
	watermark      int64
	lastDeliveryAt time.Time
	lastError      string
	failedSince    time.Time
}

type eventHub struct {
	service Service
	sender  WebhookSender
	ctx     context.Context

	mu       sync.Mutex
	subs     map[string]*eventSubscription
	verified map[string]time.Time
}

func newEventHub(ctx context.Context, service Service, sender WebhookSender) *eventHub {
	return &eventHub{
		service:  service,
		sender:   sender,
		ctx:      ctx,
		subs:     make(map[string]*eventSubscription),
		verified: make(map[string]time.Time),
	}
}

func (h *eventHub) register(server *mcp.Server, authenticated bool) error {
	if err := mcp.AddReceivingCustomMethod(server, "events/list", h.list); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomMethod(server, "events/subscribe",
		func(ctx context.Context, _ *mcp.ServerSession, p *eventsSubscribeParams) (*eventsSubscribeResult, error) {
			if !authenticated {
				return nil, rpcError(codeForbidden, "webhook subscriptions need the http transport to require a bearer token (MEOWMCP_AUTH_TOKEN)", nil)
			}
			return h.subscribe(ctx, p)
		}); err != nil {
		return err
	}
	return mcp.AddReceivingCustomMethod(server, "events/unsubscribe",
		func(_ context.Context, _ *mcp.ServerSession, p *eventsUnsubscribeParams) (*eventsUnsubscribeResult, error) {
			if !authenticated {
				return nil, rpcError(codeForbidden, "webhook subscriptions need the http transport to require a bearer token (MEOWMCP_AUTH_TOKEN)", nil)
			}
			return h.unsubscribe(p)
		})
}

func (h *eventHub) list(context.Context, *mcp.ServerSession, *eventsListParams) (*eventsListResult, error) {
	input, err := jsonschema.For[watchChatArgs](nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build input schema: %w", err)
	}
	payload, err := jsonschema.For[chatMessageEvent](nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build payload schema: %w", err)
	}
	return &eventsListResult{Events: []eventType{{
		Name: eventChatMessage,
		Description: "Watch a WhatsApp chat: fires when the other side sends messages there. Messages sent in a burst " +
			"arrive together in one event. Reading the event does not mark anything as read.",
		Delivery:      []string{"webhook"},
		InputSchema:   input,
		PayloadSchema: payload,
	}}}, nil
}

func (h *eventHub) subscribe(ctx context.Context, p *eventsSubscribeParams) (*eventsSubscribeResult, error) {
	if p == nil {
		return nil, rpcError(jsonrpc.CodeInvalidParams, "missing params", nil)
	}
	if p.Name != eventChatMessage {
		return nil, rpcError(codeNotFound, "unknown event type "+strconv.Quote(p.Name), map[string]string{"kind": "event"})
	}
	if p.Delivery.Mode != "" && p.Delivery.Mode != "webhook" {
		return nil, rpcError(codeUnsupported, "only webhook delivery is supported", map[string]string{"feature": "deliveryMode", "value": p.Delivery.Mode})
	}
	chatArg, err := parseWatchArgs(p.Arguments)
	if err != nil {
		return nil, rpcError(jsonrpc.CodeInvalidParams, err.Error(), nil)
	}
	if err := webhookprovider.ValidateURL(p.Delivery.URL); err != nil {
		return nil, rpcError(jsonrpc.CodeInvalidParams, err.Error(), nil)
	}
	secret, err := webhookprovider.ParseSecret(p.Delivery.Secret)
	if err != nil {
		return nil, rpcError(jsonrpc.CodeInvalidParams, "delivery.secret: "+err.Error(), nil)
	}
	id := subscriptionID(p.Delivery.URL, p.Name, p.Arguments)
	expiresAt := time.Now().Add(grantTTL(p.TTLMs))

	if resp, ok := h.refresh(id, secret, expiresAt); ok {
		return resp, nil
	}

	chat, aliases, err := h.service.ResolveChat(ctx, chatArg)
	switch {
	case errors.Is(err, domain.ErrInvalidRecipient), errors.Is(err, domain.ErrNotOnWhatsApp):
		return nil, rpcError(jsonrpc.CodeInvalidParams, "arguments.chat: "+err.Error(), nil)
	case err != nil:
		return nil, err
	}
	if err := h.verify(ctx, id, p.Delivery.URL, secret); err != nil {
		return nil, err
	}
	latest, err := h.service.LatestID(ctx, aliases)
	if err != nil {
		return nil, err
	}
	replayAge := maxReplayAge
	if p.MaxAgeMs != nil && *p.MaxAgeMs >= 0 {
		replayAge = min(replayAge, time.Duration(*p.MaxAgeMs)*time.Millisecond)
	}
	notBefore := time.Now().Add(-replayAge)
	watermark, truncated, err := h.startPosition(ctx, aliases, p.Cursor, latest, notBefore)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	if _, exists := h.subs[id]; exists {
		h.mu.Unlock()
		resp, _ := h.refresh(id, secret, expiresAt)
		return resp, nil
	}
	subCtx, cancel := context.WithCancel(h.ctx)
	sub := &eventSubscription{
		id:        id,
		url:       p.Delivery.URL,
		chat:      chat,
		aliases:   aliases,
		notBefore: notBefore,
		wake:      make(chan struct{}, 1),
		cancel:    cancel,
		secret:    secret,
		expiresAt: expiresAt,
		watermark: watermark,
	}
	h.subs[id] = sub
	h.mu.Unlock()

	go h.run(subCtx, sub)
	if watermark < latest {
		sub.wake <- struct{}{}
	}
	logctx.Info(ctx, "event subscription created",
		zap.String("subscription_id", id), zap.String("chat_jid", chat.JID), zap.Int64("cursor", watermark))
	return &eventsSubscribeResult{
		ID:            id,
		RefreshBefore: expiresAt.UTC().Format(timeLayout),
		Cursor:        strconv.FormatInt(watermark, 10),
		Truncated:     truncated,
	}, nil
}

func (h *eventHub) refresh(id string, secret []byte, expiresAt time.Time) (*eventsSubscribeResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub, ok := h.subs[id]
	if !ok {
		return nil, false
	}
	sub.secret, sub.expiresAt = secret, expiresAt
	status := &deliveryStatus{Active: true, LastDeliveryAt: optionalTime(sub.lastDeliveryAt)}
	if sub.lastError != "" {
		status.LastError = &sub.lastError
		status.FailedSince = optionalTime(sub.failedSince)
	}
	return &eventsSubscribeResult{
		ID:             id,
		RefreshBefore:  expiresAt.UTC().Format(timeLayout),
		Cursor:         strconv.FormatInt(sub.watermark, 10),
		DeliveryStatus: status,
	}, true
}

func (h *eventHub) startPosition(ctx context.Context, aliases []string, cursor *string, latest int64, notBefore time.Time) (int64, bool, error) {
	if cursor == nil || *cursor == "" {
		return latest, false, nil
	}
	pos, err := strconv.ParseInt(*cursor, 10, 64)
	if err != nil || pos < 0 || pos > latest {
		return latest, true, nil
	}
	next, err := h.service.MessagesSince(ctx, aliases, pos, 1)
	if err != nil {
		return 0, false, err
	}
	return pos, len(next) > 0 && next[0].Timestamp.Before(notBefore), nil
}

func (h *eventHub) unsubscribe(p *eventsUnsubscribeParams) (*eventsUnsubscribeResult, error) {
	if p == nil {
		return nil, rpcError(jsonrpc.CodeInvalidParams, "missing params", nil)
	}
	id := subscriptionID(p.Delivery.URL, p.Name, p.Arguments)
	h.mu.Lock()
	sub, ok := h.subs[id]
	delete(h.subs, id)
	h.mu.Unlock()
	if !ok {
		return nil, rpcError(codeNotFound, "no such subscription", map[string]string{"kind": "subscription"})
	}
	sub.cancel()
	return &eventsUnsubscribeResult{}, nil
}

func (h *eventHub) verify(ctx context.Context, id, url string, secret []byte) error {
	h.mu.Lock()
	until, ok := h.verified[url]
	h.mu.Unlock()
	if ok && time.Now().Before(until) {
		return nil
	}

	challenge := randomToken()
	body, err := json.Marshal(map[string]string{"type": "verification", "challenge": challenge})
	if err != nil {
		return err
	}
	resp, err := h.sender.Post(ctx, webhookprovider.Delivery{
		URL: url, Secret: secret, SubscriptionID: id, MessageID: "msg_verification_" + randomToken(), Body: body,
	})
	if err != nil {
		reason := webhookprovider.ReasonConnectionRefused
		var de *webhookprovider.DeliveryError
		if errors.As(err, &de) {
			reason = de.Reason
		}
		return rpcError(codeCallbackEndpoint, "callback endpoint failed verification: "+reason, map[string]string{"reason": reason})
	}
	var answer struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(resp, &answer) != nil || subtle.ConstantTimeCompare([]byte(answer.Challenge), []byte(challenge)) != 1 {
		return rpcError(codeCallbackEndpoint, "callback endpoint did not echo the verification challenge",
			map[string]string{"reason": webhookprovider.ReasonChallengeFailed})
	}
	h.mu.Lock()
	h.verified[url] = time.Now().Add(verificationTTL)
	h.mu.Unlock()
	return nil
}

func (h *eventHub) run(ctx context.Context, sub *eventSubscription) {
	signal, stop := h.service.Watch(sub.aliases)
	defer stop()
	expiry := time.NewTimer(time.Until(h.expiresAt(sub)))
	defer expiry.Stop()
	for {
		if !h.deliverPending(ctx, sub) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-sub.wake:
		case <-signal:
			gather(ctx, signal)
		case <-expiry.C:
			remaining := time.Until(h.expiresAt(sub))
			if remaining <= 0 {
				h.drop(sub)
				return
			}
			expiry.Reset(remaining)
		}
	}
}

func gather(ctx context.Context, signal <-chan struct{}) {
	limit := time.NewTimer(eventMaxGathering)
	defer limit.Stop()
	for {
		quiet := time.NewTimer(eventQuietWindow)
		select {
		case <-signal:
			quiet.Stop()
		case <-quiet.C:
			return
		case <-limit.C:
			quiet.Stop()
			return
		case <-ctx.Done():
			quiet.Stop()
			return
		}
	}
}

func (h *eventHub) deliverPending(ctx context.Context, sub *eventSubscription) bool {
	for {
		h.mu.Lock()
		watermark := sub.watermark
		h.mu.Unlock()
		msgs, err := h.service.MessagesSince(ctx, sub.aliases, watermark, maxEventMessages)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			logctx.Error(ctx, "failed to read messages for event", zap.String("subscription_id", sub.id), zap.Error(err))
			return true
		}
		if len(msgs) == 0 {
			return true
		}
		last := msgs[len(msgs)-1].Seq
		fresh := make([]domain.Message, 0, len(msgs))
		for _, m := range msgs {
			if !m.Timestamp.Before(sub.notBefore) {
				fresh = append(fresh, m)
			}
		}
		if len(fresh) > 0 && !h.deliver(ctx, sub, fresh, len(msgs) == maxEventMessages) {
			return false
		}
		h.mu.Lock()
		sub.watermark = last
		h.mu.Unlock()
		if len(msgs) < maxEventMessages {
			return true
		}
	}
}

func (h *eventHub) deliver(ctx context.Context, sub *eventSubscription, msgs []domain.Message, hasMore bool) bool {
	first, last := msgs[0].Seq, msgs[len(msgs)-1].Seq
	eventID := fmt.Sprintf("wa_%d_%d", first, last)
	body, err := json.Marshal(eventOccurrence{
		EventID:   eventID,
		Name:      eventChatMessage,
		Timestamp: msgs[len(msgs)-1].Timestamp.UTC().Format(timeLayout),
		Data: chatMessageEvent{
			Chat:     eventChat{JID: sub.chat.JID, Name: sub.chat.Name, Phone: phoneOf(sub.chat.JID), IsGroup: sub.chat.IsGroup},
			Messages: toMessageOutputs(msgs),
			SinceID:  last,
			HasMore:  hasMore,
		},
		Cursor: strconv.FormatInt(last, 10),
	})
	if err != nil {
		logctx.Error(ctx, "failed to encode event", zap.String("subscription_id", sub.id), zap.Error(err))
		return true
	}

	for attempt := 0; ; attempt++ {
		h.mu.Lock()
		secret := sub.secret
		h.mu.Unlock()
		_, err := h.sender.Post(ctx, webhookprovider.Delivery{
			URL: sub.url, Secret: secret, SubscriptionID: sub.id, MessageID: eventID, Body: body,
		})
		if err == nil {
			h.mu.Lock()
			sub.lastDeliveryAt, sub.lastError, sub.failedSince = time.Now(), "", time.Time{}
			h.mu.Unlock()
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		var de *webhookprovider.DeliveryError
		retryable := errors.As(err, &de) && de.Retryable
		h.mu.Lock()
		if de != nil {
			sub.lastError = de.Reason
		}
		if sub.failedSince.IsZero() {
			sub.failedSince = time.Now()
		}
		h.mu.Unlock()
		if !retryable || attempt == len(deliveryBackoff) {
			logctx.Error(ctx, "giving up on event delivery",
				zap.String("subscription_id", sub.id), zap.String("event_id", eventID), zap.Error(err))
			return true
		}
		logctx.Warn(ctx, "event delivery failed, retrying",
			zap.String("subscription_id", sub.id), zap.String("event_id", eventID),
			zap.Duration("in", deliveryBackoff[attempt]), zap.Error(err))
		select {
		case <-ctx.Done():
			return false
		case <-time.After(deliveryBackoff[attempt]):
		}
	}
}

func (h *eventHub) expiresAt(sub *eventSubscription) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return sub.expiresAt
}

func (h *eventHub) drop(sub *eventSubscription) {
	h.mu.Lock()
	if h.subs[sub.id] == sub {
		delete(h.subs, sub.id)
	}
	h.mu.Unlock()
	sub.cancel()
	logctx.Info(h.ctx, "event subscription expired", zap.String("subscription_id", sub.id))
}

func parseWatchArgs(args map[string]any) (string, error) {
	for k := range args {
		if k != "chat" {
			return "", fmt.Errorf("unknown argument %q", k)
		}
	}
	chat, _ := args["chat"].(string)
	if chat == "" {
		return "", errors.New("arguments.chat is required: a chat JID or phone number")
	}
	return chat, nil
}

func subscriptionID(url, name string, args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	canonical, _ := json.Marshal(args)
	sum := sha256.Sum256([]byte(url + "\n" + name + "\n" + string(canonical)))
	return "sub_" + hex.EncodeToString(sum[:8])
}

func grantTTL(ttlMs *int64) time.Duration {
	if ttlMs == nil || *ttlMs <= 0 {
		return defaultSubscriptionTTL
	}
	return min(max(time.Duration(*ttlMs)*time.Millisecond, minSubscriptionTTL), defaultSubscriptionTTL)
}

func optionalTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(timeLayout)
	return &s
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func rpcError(code int64, message string, data any) error {
	err := &jsonrpc.Error{Code: code, Message: message}
	if data != nil {
		err.Data, _ = json.Marshal(data)
	}
	return err
}
