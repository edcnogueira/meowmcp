package whatsappservice_test

import (
	"context"
	"os"
	"path/filepath"
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
	sent []domain.OutgoingMedia
}

func (f *sendWhatsApp) Status() domain.Status {
	return domain.Status{State: domain.ConnectionStateConnected}
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
		service = whatsappservice.New(wa, repo, nil, GinkgoT().TempDir())
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
