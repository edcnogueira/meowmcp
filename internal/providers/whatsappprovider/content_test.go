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
