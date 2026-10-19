package whatsappprovider_test

import (
	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mau.fi/whatsmeow"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
)

var _ = Describe("MediaMessage", func() {
	upload := whatsmeow.UploadResponse{DirectPath: "/v/t62/" + gofakeit.UUID(), FileLength: 42}

	It("sends a voice note as an Opus push-to-talk audio", func() {
		msg := whatsappprovider.MediaMessage(domain.OutgoingMedia{Kind: domain.MediaKindVoice, MimeType: "audio/ogg"}, upload, nil)

		Expect(msg.GetAudioMessage().GetPTT()).To(BeTrue())
		Expect(whatsappprovider.ExtractContent(msg).MediaType).To(Equal("voice"))
		Expect(whatsappprovider.MediaMimeType(whatsappprovider.MediaRef(msg))).To(Equal("audio/ogg; codecs=opus"))
	})

	It("names a document after its file and keeps the caption", func() {
		fileName, caption := gofakeit.Word()+".pdf", gofakeit.Sentence(3)

		msg := whatsappprovider.MediaMessage(domain.OutgoingMedia{
			Kind: domain.MediaKindDocument, MimeType: "application/pdf", FileName: fileName, Caption: caption,
		}, upload, nil)

		Expect(whatsappprovider.ExtractContent(msg)).To(Equal(whatsappprovider.Content{Text: fileName + " — " + caption, MediaType: "document"}))
		Expect(msg.GetDocumentMessage().GetFileLength()).To(BeEquivalentTo(upload.FileLength))
	})
})
