package sttprovider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/brianvoe/gofakeit/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/edcnogueira/meowmcp/internal/providers/sttprovider"
)

var _ = Describe("Transcribe", func() {
	var (
		audioPath string
		audio     []byte
	)

	BeforeEach(func() {
		audio = []byte(gofakeit.LetterN(256))
		audioPath = filepath.Join(GinkgoT().TempDir(), gofakeit.UUID()+".ogg")
		Expect(os.WriteFile(audioPath, audio, 0o600)).To(Succeed())
	})

	It("uploads the file with model, language and key to the transcriptions endpoint", func() {
		apiKey, model, text := gofakeit.UUID(), gofakeit.Word(), gofakeit.Sentence(5)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/v1/audio/transcriptions"))
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + apiKey))
			Expect(r.FormValue("model")).To(Equal(model))
			Expect(r.FormValue("language")).To(Equal("pt"))
			file, header, err := r.FormFile("file")
			Expect(err).NotTo(HaveOccurred())
			Expect(header.Filename).To(Equal(filepath.Base(audioPath)))
			Expect(io.ReadAll(file)).To(Equal(audio))
			_ = json.NewEncoder(w).Encode(map[string]string{"text": " " + text + "\n"})
		}))
		DeferCleanup(server.Close)

		p := sttprovider.New(sttprovider.Config{BaseURL: server.URL + "/v1/", APIKey: apiKey, Model: model, Language: "pt"})
		got, err := p.Transcribe(context.Background(), audioPath)

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(text))
	})

	It("reports the API's status and message on failure", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
		}))
		DeferCleanup(server.Close)

		p := sttprovider.New(sttprovider.Config{BaseURL: server.URL, APIKey: gofakeit.UUID(), Model: gofakeit.Word()})
		_, err := p.Transcribe(context.Background(), audioPath)

		Expect(err).To(MatchError(And(ContainSubstring("401"), ContainSubstring("Invalid API Key"))))
	})
})
