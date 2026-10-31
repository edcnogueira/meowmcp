package whatsappprovider_test

import (
	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
)

var _ = Describe("ExtractContent", func() {
	It("keeps the quoted message ID of a reply", func() {
		text, quotedID := gofakeit.Sentence(5), gofakeit.UUID()

		got := whatsappprovider.ExtractContent(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(text),
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String(quotedID)},
		}})

		Expect(got).To(Equal(whatsappprovider.Content{Text: text, QuotedID: quotedID}))
	})

	It("distinguishes voice notes from audio files", func() {
		Expect(whatsappprovider.ExtractContent(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}).MediaType).To(Equal("voice"))
		Expect(whatsappprovider.ExtractContent(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}).MediaType).To(Equal("audio"))
	})

	It("describes a document by file name and caption", func() {
		fileName, caption := gofakeit.Word()+".pdf", gofakeit.Sentence(3)

		got := whatsappprovider.ExtractContent(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			FileName: proto.String(fileName),
			Caption:  proto.String(caption),
		}})

		Expect(got.MediaType).To(Equal("document"))
		Expect(got.Text).To(Equal(fileName + " — " + caption))
	})

	It("exposes the options of a business buttons message so the agent can answer", func() {
		body, first, second := gofakeit.Sentence(4), gofakeit.Word(), gofakeit.Word()

		got := whatsappprovider.ExtractContent(&waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{
			ContentText: proto.String(body),
			Buttons: []*waE2E.ButtonsMessage_Button{
				{ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: proto.String(first)}},
				{ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: proto.String(second)}},
			},
		}})

		Expect(got.MediaType).To(Equal("buttons"))
		Expect(got.Text).To(Equal(body + "\nOptions: " + first + " | " + second))
	})

	It("uses the selected option as the text of a list reply", func() {
		title := gofakeit.Word()

		got := whatsappprovider.ExtractContent(&waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{Title: proto.String(title)}})

		Expect(got).To(Equal(whatsappprovider.Content{Text: title}))
	})

	It("reports protocol-only payloads as empty", func() {
		got := whatsappprovider.ExtractContent(&waE2E.Message{SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{}})

		Expect(got.IsEmpty()).To(BeTrue())
	})

	It("keeps who is tagged in a caption and whether the photo is view-once", func() {
		tagged := gofakeit.Numerify("###############") + "@lid"

		got := whatsappprovider.ExtractContent(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption:     proto.String("@" + tagged[:15] + " " + gofakeit.Sentence(2)),
			ViewOnce:    proto.Bool(true),
			ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{tagged}},
		}})

		Expect(got.Mentions).To(Equal([]string{tagged}))
		Expect(got.ViewOnce).To(BeTrue())
	})
})

var _ = Describe("SplitAgentName", func() {
	DescribeTable("moves a business attendant signature out of the text",
		func(text, wantAgent, wantRest string) {
			agent, rest, ok := whatsappprovider.SplitAgentName(text)
			Expect(ok).To(BeTrue())
			Expect(agent).To(Equal(wantAgent))
			Expect(rest).To(Equal(wantRest))
		},
		Entry("name and colon in bold on its own line", "*Stefanny:*\nSua consulta está confirmada.", "Stefanny", "Sua consulta está confirmada."),
		Entry("colon after the bold", "*Sthefanny*: Bom dia!", "Sthefanny", "Bom dia!"),
		Entry("accented full name", "*Ana Lúcia:* \nPode enviar o documento?", "Ana Lúcia", "Pode enviar o documento?"),
	)

	DescribeTable("leaves text without an attendant signature alone",
		func(text string) {
			agent, rest, ok := whatsappprovider.SplitAgentName(text)
			Expect(ok).To(BeFalse())
			Expect(agent).To(BeEmpty())
			Expect(rest).To(Equal(text))
		},
		Entry("bold word without colon", "*Importante* amanhã fechado"),
		Entry("signature only", "*Stefanny:*"),
		Entry("bold in the middle", "Oi *Stefanny:* tudo bem"),
		Entry("long bold sentence", "*Atenção para o novo horário de atendimento:* das 8h às 18h"),
	)
})

var _ = Describe("NormalizePhone", func() {
	DescribeTable("accepts formatted international numbers",
		func(input, want string) {
			got, err := whatsappprovider.NormalizePhone(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("plus and spaces", "+55 11 99999-8888", "5511999998888"),
		Entry("parentheses and dots", "+1 (415) 555.0100", "14155550100"),
		Entry("international dialing prefix", "0044 20 7946 0958", "442079460958"),
	)

	DescribeTable("rejects values that are not phone numbers",
		func(input string) {
			_, err := whatsappprovider.NormalizePhone(input)
			Expect(err).To(MatchError(domain.ErrInvalidRecipient))
		},
		Entry("letters", "+55 11 CLINICA"),
		Entry("too short", "+55 1199"),
		Entry("too long", "+55 11 99999 8888 12345"),
		Entry("empty", ""),
	)
})

var _ = Describe("ParseChatJID", func() {
	It("drops the device part of a user JID", func() {
		jid, err := whatsappprovider.ParseChatJID("5511999998888:12@s.whatsapp.net")
		Expect(err).NotTo(HaveOccurred())
		Expect(jid.String()).To(Equal("5511999998888@s.whatsapp.net"))
	})

	It("rejects broadcast and newsletter JIDs", func() {
		_, err := whatsappprovider.ParseChatJID("status@broadcast")
		Expect(err).To(MatchError(domain.ErrInvalidRecipient))
		_, err = whatsappprovider.ParseChatJID("123@newsletter")
		Expect(err).To(MatchError(domain.ErrInvalidRecipient))
	})
})

var _ = Describe("MediaRef", func() {
	It("keeps what is needed to download an attachment and drops thumbnails and quoted context", func() {
		directPath, mediaKey := "/v/t62/"+gofakeit.LetterN(20), []byte(gofakeit.LetterN(32))
		msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			DirectPath:    proto.String(directPath),
			MediaKey:      mediaKey,
			Mimetype:      proto.String("image/jpeg"),
			JPEGThumbnail: []byte(gofakeit.LetterN(4096)),
			ContextInfo:   &waE2E.ContextInfo{QuotedMessage: &waE2E.Message{Conversation: proto.String(gofakeit.Paragraph(3, 5, 20, " "))}},
		}}

		ref := whatsappprovider.MediaRef(msg)

		var decoded waE2E.Message
		Expect(proto.Unmarshal(ref, &decoded)).To(Succeed())
		Expect(decoded.GetImageMessage().GetDirectPath()).To(Equal(directPath))
		Expect(decoded.GetImageMessage().GetMediaKey()).To(Equal(mediaKey))
		Expect(decoded.GetImageMessage().GetJPEGThumbnail()).To(BeEmpty())
		Expect(decoded.GetImageMessage().GetContextInfo()).To(BeNil())
		Expect(msg.GetImageMessage().GetJPEGThumbnail()).NotTo(BeEmpty(), "the original message must not be modified")
		Expect(whatsappprovider.MediaMimeType(ref)).To(Equal("image/jpeg"))
	})

	It("is empty for messages without a downloadable attachment", func() {
		Expect(whatsappprovider.MediaRef(&waE2E.Message{Conversation: proto.String(gofakeit.Sentence(3))})).To(BeNil())
		Expect(whatsappprovider.MediaRef(&waE2E.Message{LocationMessage: &waE2E.LocationMessage{}})).To(BeNil())
	})
})
