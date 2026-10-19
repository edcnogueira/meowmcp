package sttprovider_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSTTProvider(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "STT Provider Suite")
}
