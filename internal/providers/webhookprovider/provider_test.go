package webhookprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/providers/webhookprovider"
)

var _ = Describe("Sign", func() {
	It("matches the Standard Webhooks reference signature", func() {
		key, err := webhookprovider.ParseSecret("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw")
		Expect(err).NotTo(HaveOccurred())

		got := webhookprovider.Sign(key, "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test": 2432232314}`))

		Expect(got).To(Equal("v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="))
	})
})

var _ = Describe("ParseSecret", func() {
	DescribeTable("rejects secrets that are not whsec_ base64 of 24 to 64 bytes",
		func(secret string) {
			_, err := webhookprovider.ParseSecret(secret)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing prefix", "MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"),
		Entry("not base64", "whsec_!!!"),
		Entry("too short", "whsec_c2hvcnQ="),
	)
})

var _ = Describe("Post", func() {
	It("refuses to deliver to a loopback address unless private callbacks are allowed", func() {
		hits := 0
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
		DeferCleanup(server.Close)

		p := webhookprovider.New(webhookprovider.Config{})
		_, err := p.Post(context.Background(), webhookprovider.Delivery{
			URL: server.URL, Secret: []byte(gofakeit.LetterN(32)), SubscriptionID: "sub_x", MessageID: "evt_x", Body: []byte(`{}`),
		})

		var de *webhookprovider.DeliveryError
		Expect(err).To(BeAssignableToTypeOf(de))
		Expect(err.(*webhookprovider.DeliveryError).Reason).To(Equal(webhookprovider.ReasonConnectionRefused))
		Expect(hits).To(BeZero())
	})
})
