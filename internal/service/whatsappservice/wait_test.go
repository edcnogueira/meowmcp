package whatsappservice_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type fakeWhatsApp struct {
	whatsappservice.WhatsApp
	pnJID, lidJID string
}

func (f *fakeWhatsApp) ResolveRecipient(context.Context, string) (string, error) { return f.pnJID, nil }
func (f *fakeWhatsApp) ChatAliases(context.Context, string) ([]string, error) {
	return []string{f.pnJID, f.lidJID}, nil
}

var _ = Describe("WaitForMessages", func() {
	var (
		ctx     context.Context
		repo    *messagesrepo.Repository
		service *whatsappservice.Service
		wa      *fakeWhatsApp
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		repo, err = messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)

		wa = &fakeWhatsApp{
			pnJID:  gofakeit.Numerify("55119########") + "@s.whatsapp.net",
			lidJID: gofakeit.Numerify("###############") + "@lid",
		}
		service = whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: GinkgoT().TempDir()})
	})

	message := func(chat string, fromMe bool, ts time.Time) domain.Message {
		return domain.Message{ID: gofakeit.UUID(), ChatJID: chat, FromMe: fromMe, Timestamp: ts.Truncate(time.Second), Text: gofakeit.Sentence(4)}
	}

	It("returns a reply already stored under the chat's LID alias after the user's last message", func() {
		sentAt := time.Now().Add(-time.Minute)
		olderIncoming := message(wa.pnJID, false, sentAt.Add(-time.Hour))
		sent := message(wa.pnJID, true, sentAt)
		reply := message(wa.lidJID, false, sentAt.Add(10*time.Second))

		By("storing history with a reply that arrived before the wait call")
		service.HandleUpdate(ctx, whatsappprovider.Update{Messages: []domain.Message{olderIncoming, sent, reply}})

		By("waiting without an explicit since_id")
		resp, err := service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{Chat: wa.pnJID, Timeout: time.Second})

		By("getting only the reply, with a cursor at it")
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.TimedOut).To(BeFalse())
		Expect(resp.Messages).To(HaveLen(1))
		Expect(resp.Messages[0].ID).To(Equal(reply.ID))
		Expect(resp.NextSinceID).To(Equal(resp.Messages[0].Seq))
	})

	It("returns a later reply that carries the same second as the one already seen, and never repeats one", func() {
		sentAt := time.Now().Add(-time.Minute)
		first := message(wa.pnJID, false, sentAt.Add(5*time.Second))
		service.HandleUpdate(ctx, whatsappprovider.Update{Messages: []domain.Message{message(wa.pnJID, true, sentAt), first}})

		seen, err := service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{Chat: wa.pnJID, Timeout: time.Second})
		Expect(err).NotTo(HaveOccurred())
		Expect(seen.Messages).To(HaveLen(1))

		By("a second message stamped with the same second arriving after the first was read")
		second := message(wa.lidJID, false, first.Timestamp)
		service.HandleUpdate(ctx, whatsappprovider.Update{Messages: []domain.Message{second}})

		next, err := service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{Chat: wa.pnJID, SinceID: seen.NextSinceID, Timeout: time.Second})
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Messages).To(HaveLen(1))
		Expect(next.Messages[0].ID).To(Equal(second.ID))

		again, err := service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{Chat: wa.pnJID, SinceID: next.NextSinceID, Timeout: 100 * time.Millisecond})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.TimedOut).To(BeTrue())
		Expect(again.Messages).To(BeEmpty())
	})

	It("wakes up on a live incoming message and ignores the user's own messages", func() {
		done := make(chan whatsappservice.WaitForMessagesResponse, 1)
		go func() {
			defer GinkgoRecover()
			resp, err := service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{Chat: wa.pnJID, Timeout: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred())
			done <- resp
		}()
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive())

		By("the user typing on the phone, which must not count as a reply")
		service.HandleUpdate(ctx, whatsappprovider.Update{Live: true, Messages: []domain.Message{message(wa.pnJID, true, time.Now().Add(time.Second))}})
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive())

		By("the other side answering")
		live := message(wa.pnJID, false, time.Now().Add(time.Second))
		service.HandleUpdate(ctx, whatsappprovider.Update{Live: true, Messages: []domain.Message{live}})

		var resp whatsappservice.WaitForMessagesResponse
		Eventually(done, 5*time.Second).Should(Receive(&resp))
		Expect(resp.Messages).To(HaveLen(1))
		Expect(resp.Messages[0].ID).To(Equal(live.ID))
	})

	It("does not wake up for history sync backfill", func() {
		go func() {
			time.Sleep(50 * time.Millisecond)
			service.HandleUpdate(ctx, whatsappprovider.Update{Live: false, Messages: []domain.Message{message(wa.pnJID, false, time.Now().Add(-24*time.Hour))}})
		}()

		resp, err := service.WaitForMessages(ctx, &whatsappservice.WaitForMessagesRequest{Chat: wa.pnJID, Timeout: 300 * time.Millisecond})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.TimedOut).To(BeTrue())
	})
})
