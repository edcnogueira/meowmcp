package whatsappprovider_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWhatsappProvider(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WhatsApp Provider Suite")
}
