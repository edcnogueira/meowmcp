package messagesrepo_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
)

var _ = Describe("Repository", func() {
	var (
		ctx  context.Context
		repo *messagesrepo.Repository
		path string
	)

	BeforeEach(func() {
		ctx = context.Background()
		path = filepath.Join(GinkgoT().TempDir(), "messages.db")
		var err error
		repo, err = messagesrepo.Open(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = repo.Close() })
	})

	newJID := func() string { return gofakeit.Numerify("55119########") + "@s.whatsapp.net" }
	at := func(offset time.Duration) time.Time { return time.Now().Add(offset).Truncate(time.Second) }

	It("keeps a chat's name when later updates carry none, and never moves activity backwards", func() {
		jid, name := newJID(), gofakeit.Company()
		Expect(repo.UpsertChat(ctx, domain.Chat{JID: jid, Name: name, LastMessageAt: at(0)})).To(Succeed())
		Expect(repo.UpsertChat(ctx, domain.Chat{JID: jid, LastMessageAt: at(-time.Hour)})).To(Succeed())

		chat, ok, err := repo.GetChat(ctx, jid)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(chat.Name).To(Equal(name))
		Expect(chat.LastMessageAt).To(BeTemporally("==", at(0)))
	})

	It("renames existing chats only", func() {
		known, unknown := newJID(), newJID()
		Expect(repo.UpsertChat(ctx, domain.Chat{JID: known})).To(Succeed())

		Expect(repo.RenameChat(ctx, known, "Dr. "+gofakeit.LastName())).To(Succeed())
		Expect(repo.RenameChat(ctx, unknown, gofakeit.Name())).To(Succeed())

		chat, _, err := repo.GetChat(ctx, known)
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Name).To(HavePrefix("Dr. "))
		_, ok, err := repo.GetChat(ctx, unknown)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("lists chats by recent activity with their newest message", func() {
		older, newer := newJID(), newJID()
		latest := domain.Message{ID: gofakeit.UUID(), ChatJID: newer, Timestamp: at(0), Text: gofakeit.Sentence(3)}
		Expect(repo.SaveMessages(ctx, []domain.Message{
			{ID: gofakeit.UUID(), ChatJID: older, Timestamp: at(-2 * time.Hour), Text: gofakeit.Sentence(3)},
			{ID: gofakeit.UUID(), ChatJID: newer, Timestamp: at(-time.Hour), Text: gofakeit.Sentence(3)},
			latest,
		})).To(Succeed())

		chats, err := repo.ListChats(ctx, &messagesrepo.ListChatsRequest{Limit: 10})

		Expect(err).NotTo(HaveOccurred())
		Expect(chats).To(HaveLen(2))
		Expect(chats[0].JID).To(Equal(newer))
		Expect(chats[0].LastMessage.ID).To(Equal(latest.ID))
		Expect(chats[1].JID).To(Equal(older))
	})

	It("returns the newest messages across chat aliases in chronological order", func() {
		pn, lid := newJID(), gofakeit.Numerify("###############")+"@lid"
		var ids []string
		for i := 5; i > 0; i-- {
			chat := pn
			if i%2 == 0 {
				chat = lid
			}
			m := domain.Message{ID: gofakeit.UUID(), ChatJID: chat, Timestamp: at(-time.Duration(i) * time.Minute), Text: gofakeit.Sentence(3)}
			ids = append(ids, m.ID)
			Expect(repo.SaveMessages(ctx, []domain.Message{m})).To(Succeed())
		}

		msgs, err := repo.ListMessages(ctx, &messagesrepo.ListMessagesRequest{ChatJIDs: []string{pn, lid}, Limit: 3})

		Expect(err).NotTo(HaveOccurred())
		got := make([]string, len(msgs))
		for i, m := range msgs {
			got[i] = m.ID
		}
		Expect(got).To(Equal(ids[2:]))
	})

	It("matches search text literally, without LIKE wildcards", func() {
		jid := newJID()
		Expect(repo.SaveMessages(ctx, []domain.Message{
			{ID: gofakeit.UUID(), ChatJID: jid, Timestamp: at(-time.Minute), Text: "100% confirmado"},
			{ID: gofakeit.UUID(), ChatJID: jid, Timestamp: at(0), Text: "1000 confirmado"},
		})).To(Succeed())

		msgs, err := repo.ListMessages(ctx, &messagesrepo.ListMessagesRequest{Query: "100%", Limit: 10})

		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Text).To(Equal("100% confirmado"))
	})

	It("reopens an existing database without re-running migrations", func() {
		jid := newJID()
		Expect(repo.UpsertChat(ctx, domain.Chat{JID: jid})).To(Succeed())
		Expect(repo.Close()).To(Succeed())

		reopened, err := messagesrepo.Open(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })
		_, ok, err := reopened.GetChat(ctx, jid)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	})
})
