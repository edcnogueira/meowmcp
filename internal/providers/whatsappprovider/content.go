package whatsappprovider

import (
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

type Content struct {
	Text      string
	MediaType string
	QuotedID  string
}

func (c Content) IsEmpty() bool {
	return c.Text == "" && c.MediaType == ""
}

func ExtractContent(m *waE2E.Message) Content {
	if m == nil {
		return Content{}
	}
	switch {
	case m.GetConversation() != "":
		return Content{Text: m.GetConversation()}
	case m.GetExtendedTextMessage() != nil:
		x := m.GetExtendedTextMessage()
		return Content{Text: x.GetText(), QuotedID: x.GetContextInfo().GetStanzaID()}
	case m.GetImageMessage() != nil:
		x := m.GetImageMessage()
		return Content{Text: x.GetCaption(), MediaType: "image", QuotedID: x.GetContextInfo().GetStanzaID()}
	case m.GetVideoMessage() != nil:
		x := m.GetVideoMessage()
		return Content{Text: x.GetCaption(), MediaType: "video", QuotedID: x.GetContextInfo().GetStanzaID()}
	case m.GetAudioMessage() != nil:
		x := m.GetAudioMessage()
		mediaType := "audio"
		if x.GetPTT() {
			mediaType = "voice"
		}
		return Content{MediaType: mediaType, QuotedID: x.GetContextInfo().GetStanzaID()}
	case m.GetDocumentMessage() != nil:
		x := m.GetDocumentMessage()
		return Content{Text: joinNonEmpty(" — ", x.GetFileName(), x.GetCaption()), MediaType: "document", QuotedID: x.GetContextInfo().GetStanzaID()}
	case m.GetStickerMessage() != nil:
		return Content{MediaType: "sticker", QuotedID: m.GetStickerMessage().GetContextInfo().GetStanzaID()}
	case m.GetLocationMessage() != nil:
		x := m.GetLocationMessage()
		return Content{
			Text:      joinNonEmpty(" — ", fmt.Sprintf("%.6f,%.6f", x.GetDegreesLatitude(), x.GetDegreesLongitude()), x.GetName(), x.GetAddress()),
			MediaType: "location",
		}
	case m.GetLiveLocationMessage() != nil:
		x := m.GetLiveLocationMessage()
		return Content{Text: fmt.Sprintf("%.6f,%.6f", x.GetDegreesLatitude(), x.GetDegreesLongitude()), MediaType: "location"}
	case m.GetContactMessage() != nil:
		return Content{Text: m.GetContactMessage().GetDisplayName(), MediaType: "contact"}
	case m.GetContactsArrayMessage() != nil:
		names := make([]string, 0, len(m.GetContactsArrayMessage().GetContacts()))
		for _, c := range m.GetContactsArrayMessage().GetContacts() {
			names = append(names, c.GetDisplayName())
		}
		return Content{Text: strings.Join(names, ", "), MediaType: "contact"}
	case pollOf(m) != nil:
		p := pollOf(m)
		opts := make([]string, 0, len(p.GetOptions()))
		for _, o := range p.GetOptions() {
			opts = append(opts, o.GetOptionName())
		}
		return Content{Text: p.GetName() + "\nOptions: " + strings.Join(opts, " | "), MediaType: "poll"}
	case m.GetButtonsMessage() != nil:
		x := m.GetButtonsMessage()
		labels := make([]string, 0, len(x.GetButtons()))
		for _, b := range x.GetButtons() {
			labels = append(labels, b.GetButtonText().GetDisplayText())
		}
		return Content{Text: withOptions(x.GetContentText(), labels), MediaType: "buttons"}
	case m.GetListMessage() != nil:
		x := m.GetListMessage()
		var rows []string
		for _, s := range x.GetSections() {
			for _, r := range s.GetRows() {
				rows = append(rows, joinNonEmpty(": ", r.GetTitle(), r.GetDescription()))
			}
		}
		return Content{Text: withOptions(joinNonEmpty("\n", x.GetTitle(), x.GetDescription()), rows), MediaType: "list"}
	case m.GetTemplateMessage() != nil:
		t := m.GetTemplateMessage().GetHydratedTemplate()
		labels := make([]string, 0, len(t.GetHydratedButtons()))
		for _, b := range t.GetHydratedButtons() {
			switch {
			case b.GetQuickReplyButton() != nil:
				labels = append(labels, b.GetQuickReplyButton().GetDisplayText())
			case b.GetUrlButton() != nil:
				labels = append(labels, b.GetUrlButton().GetDisplayText()+" ("+b.GetUrlButton().GetURL()+")")
			case b.GetCallButton() != nil:
				labels = append(labels, b.GetCallButton().GetDisplayText()+" ("+b.GetCallButton().GetPhoneNumber()+")")
			}
		}
		return Content{Text: withOptions(joinNonEmpty("\n", t.GetHydratedTitleText(), t.GetHydratedContentText(), t.GetHydratedFooterText()), labels), MediaType: "buttons"}
	case m.GetInteractiveMessage() != nil:
		x := m.GetInteractiveMessage()
		return Content{Text: joinNonEmpty("\n", x.GetHeader().GetTitle(), x.GetBody().GetText(), x.GetFooter().GetText()), MediaType: "interactive"}
	case m.GetButtonsResponseMessage() != nil:
		return Content{Text: m.GetButtonsResponseMessage().GetSelectedDisplayText(), QuotedID: m.GetButtonsResponseMessage().GetContextInfo().GetStanzaID()}
	case m.GetListResponseMessage() != nil:
		return Content{Text: m.GetListResponseMessage().GetTitle(), QuotedID: m.GetListResponseMessage().GetContextInfo().GetStanzaID()}
	case m.GetTemplateButtonReplyMessage() != nil:
		return Content{Text: m.GetTemplateButtonReplyMessage().GetSelectedDisplayText(), QuotedID: m.GetTemplateButtonReplyMessage().GetContextInfo().GetStanzaID()}
	case m.GetInteractiveResponseMessage() != nil:
		return Content{Text: m.GetInteractiveResponseMessage().GetBody().GetText(), QuotedID: m.GetInteractiveResponseMessage().GetContextInfo().GetStanzaID()}
	}
	return Content{}
}

func pollOf(m *waE2E.Message) *waE2E.PollCreationMessage {
	switch {
	case m.GetPollCreationMessage() != nil:
		return m.GetPollCreationMessage()
	case m.GetPollCreationMessageV2() != nil:
		return m.GetPollCreationMessageV2()
	case m.GetPollCreationMessageV3() != nil:
		return m.GetPollCreationMessageV3()
	}
	return nil
}

func withOptions(text string, options []string) string {
	if len(options) == 0 {
		return text
	}
	return joinNonEmpty("\n", text, "Options: "+strings.Join(options, " | "))
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
