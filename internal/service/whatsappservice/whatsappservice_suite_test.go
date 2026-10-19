package whatsappservice_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWhatsappService(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WhatsApp Service Suite")
}
