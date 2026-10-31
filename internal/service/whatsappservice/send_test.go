package whatsappservice_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mau.fi/whatsmeow"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type sendWhatsApp struct {
	*mediaWhatsApp
	sent    []domain.OutgoingMedia
	texts   []string
	sendErr error
}

func (f *sendWhatsApp) Status() domain.Status {
	return domain.Status{State: domain.ConnectionStateConnected}
}

func (f *sendWhatsApp) SendText(_ context.Context, chatJID, text string, _ *domain.Message) (domain.SentMessage, error) {
	if f.sendErr != nil {
		return domain.SentMessage{}, f.sendErr
	}
	f.texts = append(f.texts, text)
	return domain.SentMessage{ID: gofakeit.UUID(), ChatJID: chatJID, Timestamp: time.Now().Truncate(time.Second), Text: text}, nil
}

func (f *sendWhatsApp) SendMedia(_ context.Context, chatJID string, media domain.OutgoingMedia, _ *domain.Message) (domain.SentMessage, error) {
	f.sent = append(f.sent, media)
	msg := whatsappprovider.MediaMessage(media, whatsmeow.UploadResponse{DirectPath: "/v/t62/" + gofakeit.UUID()}, nil)
	content := whatsappprovider.ExtractContent(msg)
	return domain.SentMessage{
		ID:        gofakeit.UUID(),
		ChatJID:   chatJID,
		Timestamp: time.Now().Truncate(time.Second),
		Text:      content.Text,
		MediaType: content.MediaType,
		MediaRef:  whatsappprovider.MediaRef(msg),
	}, nil
}

var _ = Describe("SendFile", func() {
	var (
		ctx     context.Context
		wa      *sendWhatsApp
		service *whatsappservice.Service
		payload []byte
	)

	BeforeEach(func() {
		ctx = context.Background()
		repo, err := messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)

		payload = []byte(gofakeit.LetterN(128))
		wa = &sendWhatsApp{mediaWhatsApp: &mediaWhatsApp{
			fakeWhatsApp: &fakeWhatsApp{
				pnJID:  gofakeit.Numerify("55119########") + "@s.whatsapp.net",
				lidJID: gofakeit.Numerify("###############") + "@lid",
			},
			payload: payload,
		}}
		service = whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: GinkgoT().TempDir()})
	})

	It("sends a document from a server path and keeps it fetchable through history", func() {
		path := filepath.Join(GinkgoT().TempDir(), gofakeit.Word()+".pdf")
		Expect(os.WriteFile(path, payload, 0o600)).To(Succeed())
		caption := gofakeit.Sentence(4)

		By("sending the file")
		resp, err := service.SendFile(ctx, &whatsappservice.SendFileRequest{To: wa.pnJID, Path: path, Caption: caption})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.MimeType).To(Equal("application/pdf"))
		Expect(resp.Size).To(BeEquivalentTo(len(payload)))
		Expect(wa.sent).To(HaveLen(1))
		Expect(wa.sent[0].Kind).To(Equal(domain.MediaKindDocument))
		Expect(wa.sent[0].FileName).To(Equal(filepath.Base(path)))
		Expect(wa.sent[0].Data).To(Equal(payload))

		By("reading it back from the chat history")
		history, err := service.GetMessages(ctx, &whatsappservice.GetMessagesRequest{Chat: wa.pnJID})
		Expect(err).NotTo(HaveOccurred())
		Expect(history.Messages).To(HaveLen(1))
		Expect(history.Messages[0].FromMe).To(BeTrue())
		Expect(history.Messages[0].MediaType).To(Equal("document"))
		Expect(history.Messages[0].Text).To(Equal(filepath.Base(path) + " — " + caption))

		media, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: resp.Sent.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(media.MimeType).To(Equal("application/pdf"))
	})

	DescribeTable("picks how the file is sent from its type and send_as",
		func(fileName, sendAs string, want domain.MediaKind) {
			_, err := service.SendFile(ctx, &whatsappservice.SendFileRequest{To: wa.pnJID, Data: payload, FileName: fileName, SendAs: sendAs})
			Expect(err).NotTo(HaveOccurred())
			Expect(wa.sent).To(HaveLen(1))
			Expect(wa.sent[0].Kind).To(Equal(want))
		},
		Entry("jpeg photo", "photo.JPG", "", domain.MediaKindImage),
		Entry("png screenshot", "screen.png", "", domain.MediaKindImage),
		Entry("mp4 video", "clip.mp4", "", domain.MediaKindVideo),
		Entry("mp3 audio", "song.mp3", "", domain.MediaKindAudio),
		Entry("gif falls back to document", "anim.gif", "", domain.MediaKindDocument),
		Entry("spreadsheet", "budget.xlsx", "", domain.MediaKindDocument),
		Entry("photo forced uncompressed", "photo.jpg", "document", domain.MediaKindDocument),
		Entry("opus voice note", "note.ogg", "Voice", domain.MediaKindVoice),
	)

	DescribeTable("rejects requests WhatsApp cannot deliver as asked",
		func(req whatsappservice.SendFileRequest, wantErr string) {
			req.To = wa.pnJID
			_, err := service.SendFile(ctx, &req)
			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(wa.sent).To(BeEmpty())
		},
		Entry("both path and data", whatsappservice.SendFileRequest{Path: "/tmp/a.pdf", Data: []byte("x"), FileName: "a.pdf"}, "exactly one"),
		Entry("neither path nor data", whatsappservice.SendFileRequest{}, "exactly one"),
		Entry("data without file name", whatsappservice.SendFileRequest{Data: []byte("x")}, "file_name"),
		Entry("mp3 as voice note", whatsappservice.SendFileRequest{Data: []byte("x"), FileName: "song.mp3", SendAs: "voice"}, "audio/ogg"),
		Entry("pdf as image", whatsappservice.SendFileRequest{Data: []byte("x"), FileName: "a.pdf", SendAs: "image"}, "use send_as document"),
		Entry("unknown send_as", whatsappservice.SendFileRequest{Data: []byte("x"), FileName: "a.pdf", SendAs: "sticker"}, "send_as must be"),
		Entry("caption on audio", whatsappservice.SendFileRequest{Data: []byte("x"), FileName: "song.mp3", Caption: "hi"}, "caption is not supported"),
		Entry("missing path", whatsappservice.SendFileRequest{Path: "/nonexistent/meowmcp.pdf"}, "file_path"),
	)
})

var _ = Describe("Send approval", func() {
	var (
		ctx     context.Context
		wa      *sendWhatsApp
		service *whatsappservice.Service
		secret  []byte
		phone   string
	)

	BeforeEach(func() {
		ctx = context.Background()
		repo, err := messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)

		phone = gofakeit.Numerify("+55 11 9####-####")
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, phone)
		wa = &sendWhatsApp{mediaWhatsApp: &mediaWhatsApp{fakeWhatsApp: &fakeWhatsApp{
			pnJID:  digits + "@s.whatsapp.net",
			lidJID: gofakeit.Numerify("###############") + "@lid",
		}}}
		secret = []byte(gofakeit.LetterN(40))
		service = whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: GinkgoT().TempDir(), SendApprovalSecret: string(secret)})
	})

	approve := func(to, text string, expires time.Time) string {
		recipient, err := whatsappservice.ApprovalRecipient(to)
		Expect(err).NotTo(HaveOccurred())
		return whatsappservice.MintApproval(secret, recipient, whatsappservice.TextDigest(text), expires)
	}

	It("sends exactly the approved text once and echoes it", func() {
		text := gofakeit.Sentence(6)
		token := approve(phone, text, time.Now().Add(2*time.Minute))

		resp, err := service.SendMessage(ctx, &whatsappservice.SendMessageRequest{To: phone, Text: text, Approval: token})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Sent.Text).To(Equal(text))
		Expect(resp.Seq).To(BeNumerically(">", 0))

		By("replaying the same token")
		_, err = service.SendMessage(ctx, &whatsappservice.SendMessageRequest{To: phone, Text: text, Approval: token})
		Expect(err).To(MatchError(domain.ErrApprovalInvalid))
		Expect(wa.texts).To(Equal([]string{text}))
	})

	It("lets an approval be used again when WhatsApp did not take the message", func() {
		text := gofakeit.Sentence(6)
		token := approve(wa.pnJID, text, time.Now().Add(2*time.Minute))
		wa.sendErr = domain.ErrNotConnected

		_, err := service.SendMessage(ctx, &whatsappservice.SendMessageRequest{To: wa.pnJID, Text: text, Approval: token})
		Expect(err).To(MatchError(domain.ErrNotConnected))

		wa.sendErr = nil
		_, err = service.SendMessage(ctx, &whatsappservice.SendMessageRequest{To: wa.pnJID, Text: text, Approval: token})
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("refuses to send without a matching, fresh approval",
		func(mutate func(to, text *string) string, wantErr error) {
			to, text := phone, gofakeit.Sentence(6)
			token := mutate(&to, &text)

			_, err := service.SendMessage(ctx, &whatsappservice.SendMessageRequest{To: to, Text: text, Approval: token})

			Expect(err).To(MatchError(wantErr))
			Expect(wa.texts).To(BeEmpty())
		},
		Entry("no token", func(_, _ *string) string { return "" }, domain.ErrApprovalRequired),
		Entry("text changed after approval", func(to, text *string) string {
			token := approve(*to, *text, time.Now().Add(time.Minute))
			*text += "!"
			return token
		}, domain.ErrApprovalInvalid),
		Entry("approved for another recipient", func(to, text *string) string {
			return approve(gofakeit.Numerify("+55 21 9####-####"), *text, time.Now().Add(time.Minute))
		}, domain.ErrApprovalInvalid),
		Entry("expired", func(to, text *string) string {
			return approve(*to, *text, time.Now().Add(-time.Second))
		}, domain.ErrApprovalInvalid),
		Entry("valid for too long", func(to, text *string) string {
			return approve(*to, *text, time.Now().Add(time.Hour))
		}, domain.ErrApprovalInvalid),
		Entry("signed with another secret", func(to, text *string) string {
			recipient, _ := whatsappservice.ApprovalRecipient(*to)
			return whatsappservice.MintApproval([]byte(gofakeit.LetterN(40)), recipient, whatsappservice.TextDigest(*text), time.Now().Add(time.Minute))
		}, domain.ErrApprovalInvalid),
	)

	It("binds a file approval to its content and caption", func() {
		data, caption := []byte(gofakeit.LetterN(256)), gofakeit.Sentence(3)
		recipient, err := whatsappservice.ApprovalRecipient(phone)
		Expect(err).NotTo(HaveOccurred())
		token := whatsappservice.MintApproval(secret, recipient, whatsappservice.FileDigest(data, "report.pdf", caption), time.Now().Add(time.Minute))

		_, err = service.SendFile(ctx, &whatsappservice.SendFileRequest{To: phone, Data: append([]byte{'x'}, data...), FileName: "report.pdf", Caption: caption, Approval: token})
		Expect(err).To(MatchError(domain.ErrApprovalInvalid))

		resp, err := service.SendFile(ctx, &whatsappservice.SendFileRequest{To: phone, Data: data, FileName: "report.pdf", Caption: caption, Approval: token})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Caption).To(Equal(caption))
		Expect(resp.FileName).To(Equal("report.pdf"))
		Expect(wa.sent).To(HaveLen(1))
	})
})
