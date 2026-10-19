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
)

const MaxFileBytes = 25 << 20

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

func (p *Provider) Transcribe(ctx context.Context, path string) (string, error) {
	audio, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read audio: %w", err)
	}
	if len(audio) > MaxFileBytes {
		return "", fmt.Errorf("audio is %d MB, above the %d MB transcription limit", len(audio)>>20, MaxFileBytes>>20)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := [][2]string{{"model", p.cfg.Model}, {"response_format", "json"}}
	if p.cfg.Language != "" {
		fields = append(fields, [2]string{"language", p.cfg.Language})
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return "", fmt.Errorf("failed to build transcription request: %w", err)
		}
	}
	part, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("failed to build transcription request: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return "", fmt.Errorf("failed to build transcription request: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("failed to build transcription request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", fmt.Errorf("failed to build transcription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read transcription response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("transcription failed: %s: %s", resp.Status, strings.TrimSpace(string(raw[:min(len(raw), 512)])))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("invalid transcription response: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}
