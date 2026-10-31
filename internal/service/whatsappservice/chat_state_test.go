package whatsappservice_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type mentionsWhatsApp struct {
	*fakeWhatsApp
	known map[string]domain.Mention
}

func (f *mentionsWhatsApp) DescribeMentions(_ context.Context, jids []string) map[string]domain.Mention {
	out := make(map[string]domain.Mention, len(jids))
	for _, jid := range jids {
		if m, ok := f.known[jid]; ok {
			out[jid] = m
		}
	}
	return out
}

var _ = Describe("ChatState", func() {
	var (
		ctx     context.Context
		repo    *messagesrepo.Repository
		wa      *mentionsWhatsApp
		service *whatsappservice.Service
		base    time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		repo, err = messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)
		wa = &mentionsWhatsApp{fakeWhatsApp: &fakeWhatsApp{
			pnJID:  gofakeit.Numerify("55119########") + "@s.whatsapp.net",
			lidJID: gofakeit.Numerify("###############") + "@lid",
		}}
		service = whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: GinkgoT().TempDir()})
		base = time.Now().Add(-time.Hour).Truncate(time.Second)
	})

	msg := func(fromMe bool, offset time.Duration, text string) domain.Message {
		return domain.Message{ID: gofakeit.UUID(), ChatJID: wa.pnJID, FromMe: fromMe, Timestamp: base.Add(offset), Text: text}
	}

	It("summarizes what the other side said after the user's last message", func() {
		me := gofakeit.Numerify("###############") + "@lid"
		wa.known = map[string]domain.Mention{me: {JID: me, Name: gofakeit.FirstName(), IsMe: true}}
		question := msg(false, 2*time.Minute, "@"+me[:15]+" pode ser amanhã às 10h?")
		question.Mentions = []domain.Mention{{JID: me}}
		Expect(repo.SaveMessages(ctx, []domain.Message{
			msg(false, 0, gofakeit.Sentence(3)),
			msg(true, time.Minute, gofakeit.Sentence(3)),
			question,
			msg(false, 3*time.Minute, gofakeit.Sentence(3)),
		})).To(Succeed())

		st, err := service.ChatState(ctx, wa.pnJID)

		Expect(err).NotTo(HaveOccurred())
		Expect(st.LastOutgoing).NotTo(BeNil())
		Expect(st.Unanswered).To(HaveLen(2))
		Expect(st.LastIncoming.ID).To(Equal(st.Unanswered[1].ID))
		Expect(st.OpenQuestion).NotTo(BeNil())
		Expect(st.OpenQuestion.ID).To(Equal(question.ID))
		Expect(st.OpenQuestion.Mentions).To(ConsistOf(wa.known[me]))
		Expect(st.LastSeq).To(Equal(st.Unanswered[1].Seq))
	})

	It("has nothing unanswered once the user wrote last", func() {
		Expect(repo.SaveMessages(ctx, []domain.Message{
			msg(false, 0, "tudo certo?"),
			msg(true, time.Minute, gofakeit.Sentence(3)),
		})).To(Succeed())

		st, err := service.ChatState(ctx, wa.lidJID)

		Expect(err).NotTo(HaveOccurred())
		Expect(st.Unanswered).To(BeEmpty())
		Expect(st.OpenQuestion).To(BeNil())
		Expect(st.LastIncoming).NotTo(BeNil())
	})
})
