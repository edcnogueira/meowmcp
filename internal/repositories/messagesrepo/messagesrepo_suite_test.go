package messagesrepo_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMessagesRepo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Messages Repository Suite")
}
