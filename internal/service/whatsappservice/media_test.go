package whatsappservice_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
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
	downloads atomic.Int32
}

func (f *mediaWhatsApp) DownloadMedia(_ context.Context, _ []byte, dst *os.File) error {
	f.downloads.Add(1)
	_, err := dst.Write(f.payload)
	return err
}

type fakeTranscriber struct {
	transcript domain.Transcript
	paths      []string
}

func (f *fakeTranscriber) Transcribe(_ context.Context, path string) (domain.Transcript, error) {
	f.paths = append(f.paths, path)
	return f.transcript, nil
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
		stt = &fakeTranscriber{transcript: domain.Transcript{Text: gofakeit.Sentence(6), Unclear: true}}
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

	It("transcribes a voice note only when asked, once, and keeps the transcript and its confidence in history", func() {
		service := whatsappservice.New(wa, repo, stt, whatsappservice.Config{MediaDir: mediaDir})
		voice := store(wa.lidJID, voiceNote, "voice")

		By("downloading it through the chat's phone-number JID without asking for a transcript")
		plain, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(plain.MimeType).To(Equal("audio/ogg"))
		Expect(plain.Path).To(HavePrefix(mediaDir))
		Expect(plain.Path).To(HaveSuffix(".ogg"))
		Expect(os.ReadFile(plain.Path)).To(Equal(wa.payload))
		Expect(plain.Transcript).To(BeEmpty())
		Expect(stt.paths).To(BeEmpty())

		By("asking for the transcript")
		transcribed, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID, Transcribe: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(transcribed.Path).To(Equal(plain.Path))
		Expect(transcribed.Transcript).To(Equal(stt.transcript.Text))
		Expect(transcribed.TranscriptUnclear).To(BeTrue())

		By("asking again, with and without transcribe")
		for _, transcribe := range []bool{true, false} {
			again, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID, Transcribe: transcribe})
			Expect(err).NotTo(HaveOccurred())
			Expect(again.Transcript).To(Equal(stt.transcript.Text))
			Expect(again.TranscriptUnclear).To(BeTrue())
		}
		Expect(wa.downloads.Load()).To(BeEquivalentTo(1))
		Expect(stt.paths).To(HaveLen(1))

		By("reading the chat history")
		resp, err := service.GetMessages(ctx, &whatsappservice.GetMessagesRequest{Chat: wa.pnJID})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Messages).To(HaveLen(1))
		Expect(resp.Messages[0].Transcript).To(Equal(stt.transcript.Text))
		Expect(resp.Messages[0].TranscriptUnclear).To(BeTrue())
		Expect(resp.Messages[0].MediaStatus).To(Equal(domain.MediaStatusReady))
	})

	It("opens and stores a view-once photo as it arrives and serves the stored copy", func() {
		service := whatsappservice.New(wa, repo, stt, whatsappservice.Config{MediaDir: mediaDir})
		photo := domain.Message{
			ID:        gofakeit.UUID(),
			ChatJID:   wa.pnJID,
			Timestamp: time.Now().Truncate(time.Second),
			MediaType: "image",
			ViewOnce:  true,
			MediaRef: whatsappprovider.MediaRef(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
				Mimetype: proto.String("image/jpeg"), ViewOnce: proto.Bool(true),
			}}),
		}

		service.HandleUpdate(ctx, whatsappprovider.Update{Live: true, Messages: []domain.Message{photo}})

		By("finding it stored without anyone asking for it")
		Eventually(func() domain.MediaStatus {
			resp, err := service.GetMessages(ctx, &whatsappservice.GetMessagesRequest{Chat: wa.pnJID})
			Expect(err).NotTo(HaveOccurred())
			return resp.Messages[0].MediaStatus
		}, 5*time.Second, 20*time.Millisecond).Should(Equal(domain.MediaStatusReady))

		By("getting the stored copy, not a second download")
		resp, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.lidJID, MessageID: photo.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Message.ViewOnce).To(BeTrue())
		Expect(os.ReadFile(resp.Path)).To(Equal(wa.payload))
		Expect(wa.downloads.Load()).To(BeEquivalentTo(1))
	})

	It("reports an attachment without a download key as missing_key instead of downloadable", func() {
		service := whatsappservice.New(wa, repo, stt, whatsappservice.Config{MediaDir: mediaDir})
		old := domain.Message{ID: gofakeit.UUID(), ChatJID: wa.pnJID, Timestamp: time.Now().Add(-24 * time.Hour), MediaType: "image"}
		Expect(repo.SaveMessages(ctx, []domain.Message{old})).To(Succeed())

		history, err := service.GetMessages(ctx, &whatsappservice.GetMessagesRequest{Chat: wa.pnJID})
		Expect(err).NotTo(HaveOccurred())
		Expect(history.Messages[0].MediaStatus).To(Equal(domain.MediaStatusMissingKey))

		_, err = service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: old.ID})
		Expect(err).To(MatchError(domain.ErrMediaMissingKey))
		Expect(wa.downloads.Load()).To(BeZero())
	})

	It("still returns the audio file when transcription is not configured", func() {
		service := whatsappservice.New(wa, repo, nil, whatsappservice.Config{MediaDir: mediaDir})
		voice := store(wa.pnJID, voiceNote, "voice")

		resp, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: voice.ID, Transcribe: true})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Path).To(BeAnExistingFile())
		Expect(resp.Transcript).To(BeEmpty())
		Expect(resp.TranscriptionError).To(ContainSubstring("MEOWMCP_STT_API_KEY"))
	})

	It("does not transcribe images", func() {
		service := whatsappservice.New(wa, repo, stt, whatsappservice.Config{MediaDir: mediaDir})
		image := store(wa.pnJID, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}, "image")

		resp, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: image.ID, Transcribe: true})

		Expect(err).NotTo(HaveOccurred())
		Expect(resp.MimeType).To(Equal("image/jpeg"))
		Expect(resp.Path).To(HaveSuffix(".jpg"))
		Expect(stt.paths).To(BeEmpty())
	})

	It("rejects messages without an attachment", func() {
		service := whatsappservice.New(wa, repo, stt, whatsappservice.Config{MediaDir: mediaDir})
		text := domain.Message{ID: gofakeit.UUID(), ChatJID: wa.pnJID, Timestamp: time.Now(), Text: gofakeit.Sentence(3)}
		Expect(repo.SaveMessages(ctx, []domain.Message{text})).To(Succeed())

		_, err := service.GetMedia(ctx, &whatsappservice.GetMediaRequest{Chat: wa.pnJID, MessageID: text.ID})

		Expect(err).To(MatchError(domain.ErrNoMedia))
		Expect(wa.downloads.Load()).To(BeZero())
	})
})
