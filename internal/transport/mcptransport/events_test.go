package mcptransport_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/webhookprovider"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
	"github.com/edcnogueira/meowmcp/internal/transport/mcptransport"
)

type fakeWhatsApp struct {
	whatsappservice.WhatsApp
	pnJID, lidJID string
}

func (f *fakeWhatsApp) ResolveRecipient(context.Context, string) (string, error) { return f.pnJID, nil }
func (f *fakeWhatsApp) ChatAliases(context.Context, string) ([]string, error) {
	return []string{f.pnJID, f.lidJID}, nil
}

type subscribeParams struct {
	mcp.ParamsBase
	Name      string            `json:"name"`
	Arguments map[string]any    `json:"arguments"`
	Delivery  map[string]string `json:"delivery"`
	TTLMs     int64             `json:"ttlMs"`
}

type subscribeResult struct {
	mcp.ResultBase
	ID             string         `json:"id"`
	RefreshBefore  string         `json:"refreshBefore"`
	Cursor         string         `json:"cursor"`
	DeliveryStatus map[string]any `json:"deliveryStatus"`
}

type delivery struct {
	headers http.Header
	body    map[string]any
}

type receiver struct {
	key []byte

	mu     sync.Mutex
	events []delivery
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer GinkgoRecover()
	raw, err := io.ReadAll(req.Body)
	Expect(err).NotTo(HaveOccurred())
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte(req.Header.Get("webhook-id") + "." + req.Header.Get("webhook-timestamp") + "."))
	mac.Write(raw)
	Expect(req.Header.Get("webhook-signature")).To(Equal("v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))))

	var body map[string]any
	Expect(json.Unmarshal(raw, &body)).To(Succeed())
	if body["type"] == "verification" {
		_ = json.NewEncoder(w).Encode(map[string]any{"challenge": body["challenge"]})
		return
	}
	r.mu.Lock()
	r.events = append(r.events, delivery{headers: req.Header.Clone(), body: body})
	r.mu.Unlock()
}

func (r *receiver) received() []delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delivery(nil), r.events...)
}

var _ = Describe("chat.message_received events", func() {
	var (
		ctx     context.Context
		service *whatsappservice.Service
		wa      *fakeWhatsApp
		hook    *receiver
		hookURL string
		secret  string
		session *mcp.ClientSession
	)

	BeforeEach(func() {
		ctx = context.Background()
		repo, err := messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)
		wa = &fakeWhatsApp{
			pnJID:  gofakeit.Numerify("55119########") + "@s.whatsapp.net",
			lidJID: gofakeit.Numerify("###############") + "@lid",
		}
		service = whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: GinkgoT().TempDir()})

		key := make([]byte, 32)
		_, _ = rand.Read(key)
		secret = "whsec_" + base64.StdEncoding.EncodeToString(key)
		hook = &receiver{key: key}
		server := httptest.NewTLSServer(hook)
		DeferCleanup(server.Close)
		hookURL = server.URL + "/webhooks/" + gofakeit.LetterN(8)
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())

		transport := mcptransport.New(service, mcptransport.Options{
			Webhooks: webhookprovider.New(webhookprovider.Config{AllowPrivate: true, TLSConfig: &tls.Config{RootCAs: roots}}),
		})
		DeferCleanup(transport.Close)
		mcpServer, err := transport.Server()
		Expect(err).NotTo(HaveOccurred())
		serverSide, clientSide := mcp.NewInMemoryTransports()
		serverSession, err := mcpServer.Connect(ctx, serverSide, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(serverSession.Close)

		client := mcp.NewClient(&mcp.Implementation{Name: "hermes-test", Version: "1"}, nil)
		Expect(mcp.AddSendingCustomMethod[*subscribeParams, *subscribeResult](client, "events/subscribe")).To(Succeed())
		session, err = client.Connect(ctx, clientSide, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Close)
	})

	subscribe := func(name string) (*subscribeResult, error) {
		return mcp.CallCustomMethod[*subscribeParams, *subscribeResult](ctx, session, "events/subscribe", &subscribeParams{
			Name:      name,
			Arguments: map[string]any{"chat": "+" + strings.TrimSuffix(wa.pnJID, "@s.whatsapp.net")},
			Delivery:  map[string]string{"mode": "webhook", "url": hookURL, "secret": secret},
			TTLMs:     3600_000,
		})
	}

	incoming := func(chat string) domain.Message {
		return domain.Message{ID: gofakeit.UUID(), ChatJID: chat, Timestamp: time.Now().Truncate(time.Second), Text: gofakeit.Sentence(4)}
	}

	It("delivers a burst of replies as one signed event that starts after the subscription", func() {
		By("having history from before the subscription")
		service.HandleUpdate(ctx, whatsappprovider.Update{Live: true, Messages: []domain.Message{incoming(wa.pnJID)}})

		sub, err := subscribe("chat.message_received")
		Expect(err).NotTo(HaveOccurred())
		Expect(sub.ID).To(HavePrefix("sub_"))
		Expect(time.Parse(time.RFC3339, sub.RefreshBefore)).To(BeTemporally("~", time.Now().Add(time.Hour), time.Minute))

		By("the other side answering in two messages, one under the LID alias, while the user also types")
		first, second := incoming(wa.pnJID), incoming(wa.lidJID)
		own := incoming(wa.pnJID)
		own.FromMe = true
		service.HandleUpdate(ctx, whatsappprovider.Update{Live: true, Messages: []domain.Message{first, own}})
		service.HandleUpdate(ctx, whatsappprovider.Update{Live: true, Messages: []domain.Message{second}})

		Eventually(hook.received, 15*time.Second, 100*time.Millisecond).Should(HaveLen(1))
		Consistently(hook.received, 5*time.Second, 250*time.Millisecond).Should(HaveLen(1))

		event := hook.received()[0]
		Expect(event.headers.Get("X-MCP-Subscription-Id")).To(Equal(sub.ID))
		Expect(event.headers.Get("webhook-id")).To(Equal(event.body["eventId"]))
		Expect(event.body["name"]).To(Equal("chat.message_received"))
		data := event.body["data"].(map[string]any)
		Expect(data["marked_read"]).To(BeFalse())
		Expect(data["chat"].(map[string]any)["jid"]).To(Equal(wa.pnJID))
		msgs := data["messages"].([]any)
		Expect(msgs).To(HaveLen(2))
		Expect(msgs[0].(map[string]any)["id"]).To(Equal(first.ID))
		Expect(msgs[1].(map[string]any)["id"]).To(Equal(second.ID))
		Expect(data["since_id"]).To(BeEquivalentTo(msgs[1].(map[string]any)["seq"]))
		Expect(event.body["cursor"]).To(Equal(strconv.FormatInt(int64(data["since_id"].(float64)), 10)))

		By("refreshing the same subscription")
		again, err := subscribe("chat.message_received")
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ID).To(Equal(sub.ID))
		Expect(again.Cursor).To(Equal(event.body["cursor"]))
		Expect(again.DeliveryStatus["active"]).To(BeTrue())
	})

	It("rejects an unknown event type with the NotFound code", func() {
		_, err := subscribe("chat." + gofakeit.Word())

		var rpcErr *jsonrpc.Error
		Expect(errors.As(err, &rpcErr)).To(BeTrue())
		Expect(rpcErr.Code).To(BeEquivalentTo(-32023))
	})
})
