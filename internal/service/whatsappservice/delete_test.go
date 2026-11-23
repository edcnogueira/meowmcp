package whatsappservice_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type deleteWhatsApp struct {
	*fakeWhatsApp
	chats   []string
	deleted []string
	err     error
}

func (f *deleteWhatsApp) DeleteForMe(_ context.Context, chatJID string, msgs []domain.Message) error {
	if f.err != nil {
		return f.err
	}
	f.chats = append(f.chats, chatJID)
	for _, m := range msgs {
		f.deleted = append(f.deleted, m.ID)
	}
	return nil
}

var _ = Describe("DeleteForMe", func() {
	var (
		ctx     context.Context
		repo    *messagesrepo.Repository
		wa      *deleteWhatsApp
		service *whatsappservice.Service
		day     time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		repo, err = messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)
		wa = &deleteWhatsApp{fakeWhatsApp: &fakeWhatsApp{
			pnJID:  gofakeit.Numerify("55119########") + "@s.whatsapp.net",
			lidJID: gofakeit.Numerify("###############") + "@lid",
		}}
		service = whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: GinkgoT().TempDir()})
		now := time.Now().AddDate(0, 0, -3)
		day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	})

	message := func(chat string, ts time.Time) domain.Message {
		return domain.Message{ID: gofakeit.UUID(), ChatJID: chat, Timestamp: ts, Text: gofakeit.Sentence(4)}
	}
	remaining := func() []string {
		msgs, err := repo.ListMessages(ctx, &messagesrepo.ListMessagesRequest{Limit: 1000})
		Expect(err).NotTo(HaveOccurred())
		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		return ids
	}

	It("deletes the named messages on WhatsApp and locally, with their stored media", func() {
		kept, text, photo := message(wa.pnJID, day), message(wa.pnJID, day.Add(time.Minute)), message(wa.lidJID, day.Add(2*time.Minute))
		Expect(repo.SaveMessages(ctx, []domain.Message{kept, text, photo})).To(Succeed())
		file := filepath.Join(GinkgoT().TempDir(), gofakeit.UUID()+".jpg")
		Expect(os.WriteFile(file, []byte(gofakeit.LetterN(16)), 0o600)).To(Succeed())
		Expect(repo.SetMediaFile(ctx, []string{wa.lidJID}, photo.ID, file)).To(Succeed())

		resp, err := service.DeleteForMe(ctx, &whatsappservice.DeleteForMeRequest{Chat: wa.pnJID, MessageIDs: []string{text.ID, photo.ID, text.ID}})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Deleted).To(Equal(2))
		Expect(wa.chats).To(ConsistOf(wa.pnJID))
		Expect(wa.deleted).To(ConsistOf(text.ID, photo.ID))
		Expect(remaining()).To(ConsistOf(kept.ID))
		Expect(file).NotTo(BeAnExistingFile())
	})

	It("wipes whole days and keeps the messages around them", func() {
		before, after := message(wa.pnJID, day.Add(-time.Second)), message(wa.pnJID, day.AddDate(0, 0, 1))
		other := message(gofakeit.Numerify("55119########")+"@s.whatsapp.net", day.Add(time.Hour))
		msgs := []domain.Message{before, after, other}
		for i := range 250 {
			msgs = append(msgs, message(wa.pnJID, day.Add(time.Duration(i)*time.Minute)))
		}
		Expect(repo.SaveMessages(ctx, msgs)).To(Succeed())

		resp, err := service.DeleteForMe(ctx, &whatsappservice.DeleteForMeRequest{Chat: wa.pnJID, Days: []time.Time{day}})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Deleted).To(Equal(250))
		Expect(wa.deleted).To(HaveLen(250))
		Expect(remaining()).To(ConsistOf(before.ID, after.ID, other.ID))
	})

	It("keeps the local history when WhatsApp does not take the deletion", func() {
		m := message(wa.pnJID, day)
		Expect(repo.SaveMessages(ctx, []domain.Message{m})).To(Succeed())
		wa.err = errors.New(gofakeit.Sentence(3))

		_, err := service.DeleteForMe(ctx, &whatsappservice.DeleteForMeRequest{Chat: wa.pnJID, MessageIDs: []string{m.ID}})

		Expect(err).To(MatchError(wa.err))
		Expect(remaining()).To(ConsistOf(m.ID))
	})

	It("deletes nothing when one of the messages is unknown", func() {
		m := message(wa.pnJID, day)
		Expect(repo.SaveMessages(ctx, []domain.Message{m})).To(Succeed())

		_, err := service.DeleteForMe(ctx, &whatsappservice.DeleteForMeRequest{Chat: wa.pnJID, MessageIDs: []string{m.ID, gofakeit.UUID()}})

		Expect(err).To(MatchError(domain.ErrMessageNotFound))
		Expect(wa.deleted).To(BeEmpty())
		Expect(remaining()).To(ConsistOf(m.ID))
	})
})
