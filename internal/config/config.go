package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

type Config struct {
	Transport string
	HTTPAddr  string
	AuthToken string
	DataDir   string
	LogLevel  string
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
