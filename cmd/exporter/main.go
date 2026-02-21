package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jasongodson/cloudflare-metrics-exporter/internal/cloudflare"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/config"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/httpserver"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/logging"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/metrics"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/poller"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}

	registry := metrics.NewRegistry()
	cfClient := cloudflare.NewClient(cfg.APIToken, cfg.RequestTimeout, cloudflare.Options{Logger: logger})
	cfPoller := poller.New(cfg, cfClient, registry, logger)

	var ready atomic.Bool
	ready.Store(true)

	httpSrv := httpserver.New(cfg.HTTPAddr, registry.Gatherer(), ready.Load, logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go cfPoller.Run(ctx)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- httpSrv.Start()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	return nil
}
