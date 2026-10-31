package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jamillosantos/logctx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/config"
	"github.com/edcnogueira/meowmcp/internal/logging"
	"github.com/edcnogueira/meowmcp/internal/providers/sttprovider"
	"github.com/edcnogueira/meowmcp/internal/providers/webhookprovider"
	"github.com/edcnogueira/meowmcp/internal/providers/whatsappprovider"
	"github.com/edcnogueira/meowmcp/internal/repositories/messagesrepo"
	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
	"github.com/edcnogueira/meowmcp/internal/transport/mcptransport"
)

var version = "dev"

func main() {
	cfg, err := config.Load(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := logctx.Initialize(logctx.WithDefaultLogger(logger)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = logctx.WithLogger(ctx, logger)

	if err := run(ctx, cfg, logger); err != nil {
		logctx.Error(ctx, "meowmcp stopped with error", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, logger *zap.Logger) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("failed to create data dir: %w", err)
	}

	messages, err := messagesrepo.Open(ctx, filepath.Join(cfg.DataDir, "messages.db"))
	if err != nil {
		return err
	}
	defer messages.Close()

	wa, err := whatsappprovider.New(ctx, messagesrepo.DSN(filepath.Join(cfg.DataDir, "session.db")), logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := wa.Stop(); err != nil {
			logctx.Error(ctx, "failed to stop whatsapp provider", zap.Error(err))
		}
	}()

	var transcriber whatsappservice.Transcriber
	if cfg.STT.APIKey != "" {
		transcriber = sttprovider.New(sttprovider.Config{
			BaseURL:  cfg.STT.BaseURL,
			APIKey:   cfg.STT.APIKey,
			Model:    cfg.STT.Model,
			Language: cfg.STT.Language,
		})
	}
	service := whatsappservice.New(wa, messages, transcriber, whatsappservice.Config{
		MediaDir:           filepath.Join(cfg.DataDir, "media"),
		SendApprovalSecret: cfg.SendApprovalSecret,
	})
	wa.SetUpdateHandler(service.HandleUpdate)
	wa.Start(ctx)

	transport := mcptransport.New(service, mcptransport.Options{
		QRImagePath: filepath.Join(cfg.DataDir, "qr.png"),
		Version:     version,
		Webhooks:    webhookprovider.New(webhookprovider.Config{AllowPrivate: cfg.EventsAllowPrivateCallback}),
	})
	defer transport.Close()
	logctx.Info(ctx, "meowmcp starting",
		zap.String("version", version),
		zap.String("transport", cfg.Transport),
		zap.String("data_dir", cfg.DataDir),
		zap.Bool("transcription", transcriber != nil),
		zap.Bool("send_approval", service.SendApprovalRequired()),
		zap.String("whatsapp_state", service.Status().State.String()))
	if !service.SendApprovalRequired() {
		logctx.Warn(ctx, "send approval is off: any MCP caller can send messages; set MEOWMCP_SEND_APPROVAL_SECRET to require approval tokens")
	}

	switch cfg.Transport {
	case config.TransportHTTP:
		return serveHTTP(ctx, cfg, transport)
	default:
		server, err := transport.Server()
		if err != nil {
			return err
		}
		err = server.Run(ctx, &mcp.StdioTransport{})
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func serveHTTP(ctx context.Context, cfg config.Config, transport *mcptransport.Transport) error {
	handler, err := transport.HTTPHandler(cfg.AuthToken)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logctx.Info(ctx, "mcp http server listening", zap.String("url", "http://"+cfg.HTTPAddr+"/mcp"))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server failed: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shut down http server: %w", err)
	}
	return nil
}
