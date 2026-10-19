package whatsappservice_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type mediaWhatsApp struct {
	*fakeWhatsApp
	payload   []byte
	downloads int
}

func (f *mediaWhatsApp) DownloadMedia(_ context.Context, _ []byte, dst *os.File) error {
	f.downloads++
	_, err := dst.Write(f.payload)
	return err
}

type fakeTranscriber struct {
	text  string
	paths []string
}

func (f *fakeTranscriber) Transcribe(_ context.Context, path string) (string, error) {
	f.paths = append(f.paths, path)
	return f.text, nil
}

var _ = Describe("GetMedia", func() {
	var (
		ctx      context.Context
		repo     *messagesrepo.Repository
		wa       *mediaWhatsApp
		stt      *fakeTranscriber
		mediaDir string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		repo, err = messagesrepo.Open(ctx, filepath.Join(GinkgoT().TempDir(), "messages.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(repo.Close)

		wa = &mediaWhatsApp{
			fakeWhatsApp: &fakeWhatsApp{
				pnJID:  gofakeit.Numerify("55119########") + "@s.whatsapp.net",
				lidJID: gofakeit.Numerify("###############") + "@lid",
			},
			payload: []byte(gofakeit.LetterN(64)),
		}
		stt = &fakeTranscriber{text: gofakeit.Sentence(6)}
		mediaDir = GinkgoT().TempDir()
	})

	store := func(chat string, attachment *waE2E.Message, mediaType string) domain.Message {
		m := domain.Message{
			ID:        gofakeit.UUID(),
			ChatJID:   chat,
			Timestamp: time.Now().Truncate(time.Second),
			MediaType: mediaType,
			MediaRef:  whatsappprovider.MediaRef(attachment),
		}
		Expect(repo.SaveMessages(ctx, []domain.Message{m})).To(Succeed())
		return m
	}
	voiceNote := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		PTT:        proto.Bool(true),
		Mimetype:   proto.String("audio/ogg; codecs=opus"),
		DirectPath: proto.String("/v/t62/voice"),
	}}

	It("downloads a voice note once, transcribes it once and exposes the transcript in history", func() {
		service := whatsappservice.New(wa, repo, stt, mediaDir)
		voice := store(wa.lidJID, voiceNote, "voice")

		By("asking for the media through the chat's phone-number JID")
		first, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.MimeType).To(Equal("audio/ogg"))
		Expect(first.Path).To(HavePrefix(mediaDir))
		Expect(first.Path).To(HaveSuffix(".ogg"))
		Expect(os.ReadFile(first.Path)).To(Equal(wa.payload))
		Expect(first.Transcript).To(Equal(stt.text))

		By("asking again")
		second, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Path).To(Equal(first.Path))
		Expect(second.Transcript).To(Equal(stt.text))
		Expect(wa.downloads).To(Equal(1))
		Expect(stt.paths).To(HaveLen(1))

		By("reading the chat history")
		resp, err := service.GetMessages(ctx, &whatsappservice.GetMessagesRequest{Chat: wa.pnJID})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Messages).To(HaveLen(1))
		Expect(resp.Messages[0].Transcript).To(Equal(stt.text))
	})

	It("still returns the audio file when transcription is not configured", func() {
		service := whatsappservice.New(wa, repo, nil, mediaDir)
		voice := store(wa.pnJID, voiceNote, "voice")

		resp, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Path).To(BeAnExistingFile())
		Expect(resp.Transcript).To(BeEmpty())
		Expect(resp.TranscriptionError).To(ContainSubstring("MEOWMCP_STT_API_KEY"))
	})

	It("does not transcribe images", func() {
		service := whatsappservice.New(wa, repo, stt, mediaDir)
		image := store(wa.pnJID, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}, "image")

		resp, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: image.ID})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.MimeType).To(Equal("image/jpeg"))
		Expect(resp.Path).To(HaveSuffix(".jpg"))
		Expect(stt.paths).To(BeEmpty())
	})

	It("rejects messages without an attachment", func() {
		service := whatsappservice.New(wa, repo, stt, mediaDir)
		text := domain.Message{ID: gofakeit.UUID(), ChatJID: wa.pnJID, Timestamp: time.Now(), Text: gofakeit.Sentence(3)}
		Expect(repo.SaveMessages(ctx, []domain.Message{text})).To(Succeed())

		_, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: text.ID})

		Expect(err).To(MatchError(domain.ErrNoMedia))
		Expect(wa.downloads).To(BeZero())
	})
})
