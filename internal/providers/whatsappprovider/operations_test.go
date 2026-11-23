package whatsappprovider_test

import (
	"time"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
)

var _ = Describe("DeleteForMePatch", func() {
	user := types.NewJID(gofakeit.Numerify("55119########"), types.DefaultUserServer)
	group := types.NewJID(gofakeit.Numerify("120363############"), types.GroupServer)
	member := gofakeit.Numerify("55119########") + "@s.whatsapp.net"

	DescribeTable("addresses each message the way the other devices key it",
		func(chat types.JID, fromMe bool, wantFromMe, wantSender string) {
			m := domain.Message{ID: gofakeit.UUID(), SenderJID: member, FromMe: fromMe, Timestamp: time.Now().Truncate(time.Second)}

			patch := whatsappprovider.DeleteForMePatch(chat, []domain.Message{m})

			Expect(patch.Type).To(Equal(appstate.WAPatchRegularHigh))
			Expect(patch.Mutations).To(HaveLen(1))
			Expect(patch.Mutations[0].Index).To(Equal([]string{"deleteMessageForMe", chat.String(), m.ID, wantFromMe, wantSender}))
			action := patch.Mutations[0].Value.GetDeleteMessageForMeAction()
			Expect(action.GetMessageTimestamp()).To(Equal(m.Timestamp.Unix()))
			Expect(action.GetDeleteMedia()).To(BeTrue())
		},
		Entry("received in a direct chat", user, false, "0", "0"),
		Entry("sent in a direct chat", user, true, "1", "0"),
		Entry("received in a group", group, false, "0", member),
		Entry("sent in a group", group, true, "1", "0"),
	)
})
