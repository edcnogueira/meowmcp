package sttprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

const MaxFileBytes = 25 << 20

const (
	unclearLogProb          = -1.0
	unclearNoSpeechProb     = 0.6
	unclearCompressionRatio = 2.4
)

type Config struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
}

type Provider struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Provider {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg, http: &http.Client{Timeout: 2 * time.Minute}}
}

func (p *Provider) Transcribe(ctx context.Context, path string) (domain.Transcript, error) {
	audio, err := os.ReadFile(path)
	if err != nil {
		return domain.Transcript{}, fmt.Errorf("failed to read audio: %w", err)
	}
	if len(audio) > MaxFileBytes {
		return domain.Transcript{}, fmt.Errorf("audio is %d MB, above the %d MB transcription limit", len(audio)>>20, MaxFileBytes>>20)
	}

	status, raw, err := p.request(ctx, path, audio, "verbose_json")
	if err == nil && status == http.StatusBadRequest && strings.Contains(string(raw), "response_format") {
		status, raw, err = p.request(ctx, path, audio, "json")
	}
	if err != nil {
		return domain.Transcript{}, err
	}
	if status != http.StatusOK {
		return domain.Transcript{}, fmt.Errorf("transcription failed: %d %s: %s", status, http.StatusText(status), strings.TrimSpace(string(raw[:min(len(raw), 512)])))
	}
	var out struct {
		Text     string `json:"text"`
		Segments []struct {
			AvgLogProb       float64 `json:"avg_logprob"`
			NoSpeechProb     float64 `json:"no_speech_prob"`
			CompressionRatio float64 `json:"compression_ratio"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return domain.Transcript{}, fmt.Errorf("invalid transcription response: %w", err)
	}
	t := domain.Transcript{Text: strings.TrimSpace(out.Text)}
	for _, s := range out.Segments {
		if s.AvgLogProb < unclearLogProb || s.NoSpeechProb > unclearNoSpeechProb || s.CompressionRatio > unclearCompressionRatio {
			t.Unclear = true
		}
	}
	return t, nil
}

func (p *Provider) request(ctx context.Context, path string, audio []byte, format string) (int, []byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := [][2]string{{"model", p.cfg.Model}, {"response_format", format}}
	if p.cfg.Language != "" {
		fields = append(fields, [2]string{"language", p.cfg.Language})
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return 0, nil, fmt.Errorf("failed to build transcription request: %w", err)
		}
	}
	part, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to build transcription request: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return 0, nil, fmt.Errorf("failed to build transcription request: %w", err)
	}
	if err := mw.Close(); err != nil {
		return 0, nil, fmt.Errorf("failed to build transcription request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to build transcription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := p.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read transcription response: %w", err)
	}
	return resp.StatusCode, raw, nil
}
