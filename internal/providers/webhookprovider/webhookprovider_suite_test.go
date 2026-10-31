package webhookprovider_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWebhookProvider(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Provider Suite")
}
