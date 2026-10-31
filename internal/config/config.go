package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

type Config struct {
	Transport                  string
	HTTPAddr                   string
	AuthToken                  string
	DataDir                    string
	LogLevel                   string
	SendApprovalSecret         string
	EventsAllowPrivateCallback bool
	STT                        STTConfig
}

type STTConfig struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
}

func Load(args []string) (Config, error) {
	defaultDataDir := "./data"
	if home, err := os.UserHomeDir(); err == nil {
		defaultDataDir = filepath.Join(home, ".meowmcp")
	}

	var cfg Config
	fs := flag.NewFlagSet("meowmcp", flag.ContinueOnError)
	fs.StringVar(&cfg.Transport, "transport", envOr("MEOWMCP_TRANSPORT", TransportStdio), "MCP transport: stdio or http (env MEOWMCP_TRANSPORT)")
	fs.StringVar(&cfg.HTTPAddr, "addr", envOr("MEOWMCP_HTTP_ADDR", "127.0.0.1:8484"), "listen address for the http transport (env MEOWMCP_HTTP_ADDR)")
	fs.StringVar(&cfg.AuthToken, "auth-token", os.Getenv("MEOWMCP_AUTH_TOKEN"), "bearer token required by the http transport (env MEOWMCP_AUTH_TOKEN)")
	fs.StringVar(&cfg.DataDir, "data-dir", envOr("MEOWMCP_DATA_DIR", defaultDataDir), "directory for the session and message databases (env MEOWMCP_DATA_DIR)")
	fs.StringVar(&cfg.LogLevel, "log-level", envOr("MEOWMCP_LOG_LEVEL", "info"), "log level: debug, info, warn, error (env MEOWMCP_LOG_LEVEL)")
	fs.StringVar(&cfg.STT.BaseURL, "stt-url", envOr("MEOWMCP_STT_URL", "https://api.groq.com/openai/v1"), "OpenAI-compatible base URL for voice transcription (env MEOWMCP_STT_URL)")
	fs.StringVar(&cfg.STT.APIKey, "stt-api-key", os.Getenv("MEOWMCP_STT_API_KEY"), "API key for voice transcription; empty disables it (env MEOWMCP_STT_API_KEY)")
	fs.StringVar(&cfg.STT.Model, "stt-model", envOr("MEOWMCP_STT_MODEL", "whisper-large-v3-turbo"), "transcription model (env MEOWMCP_STT_MODEL)")
	fs.StringVar(&cfg.STT.Language, "stt-language", os.Getenv("MEOWMCP_STT_LANGUAGE"), "ISO-639-1 language hint, e.g. pt; empty auto-detects (env MEOWMCP_STT_LANGUAGE)")
	fs.StringVar(&cfg.SendApprovalSecret, "send-approval-secret", os.Getenv("MEOWMCP_SEND_APPROVAL_SECRET"), "shared secret for send approval tokens; when set, send_message and send_file refuse to send without a valid token (env MEOWMCP_SEND_APPROVAL_SECRET)")
	fs.BoolVar(&cfg.EventsAllowPrivateCallback, "events-allow-private-callbacks", envBool("MEOWMCP_EVENTS_ALLOW_PRIVATE_CALLBACKS"), "allow event webhooks to private, loopback and CGNAT addresses (env MEOWMCP_EVENTS_ALLOW_PRIVATE_CALLBACKS)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Transport {
	case TransportStdio:
	case TransportHTTP:
		host, _, err := net.SplitHostPort(c.HTTPAddr)
		if err != nil {
			return fmt.Errorf("invalid -addr %q: %w", c.HTTPAddr, err)
		}
		if c.AuthToken == "" && !isLoopback(host) {
			return errors.New("-auth-token (MEOWMCP_AUTH_TOKEN) is required when the http transport listens on a non-loopback address")
		}
	default:
		return fmt.Errorf("invalid -transport %q: use %s or %s", c.Transport, TransportStdio, TransportHTTP)
	}
	if c.DataDir == "" {
		return errors.New("-data-dir must not be empty")
	}
	if c.SendApprovalSecret != "" && len(c.SendApprovalSecret) < 32 {
		return errors.New("-send-approval-secret (MEOWMCP_SEND_APPROVAL_SECRET) must be at least 32 characters")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	return err == nil && v
}
