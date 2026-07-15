// Command server runs the UI backend: Kubernetes REST/streaming API, agent
// proxy, and static hosting for the built frontend.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emreoztoprak/kentinel/internal/config"
	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/logging"
	"github.com/emreoztoprak/kentinel/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}
	log := logging.New(cfg.LogLevel, cfg.LogFormat)

	client, err := k8s.NewClient(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("connecting to kubernetes: %w (is a kubeconfig available, or are we running in-cluster?)", err)
	}

	staticDir := staticDirIfPresent(log)
	srv := server.New(client, cfg.AgentURL, staticDir, log)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("server listening", "port", cfg.Port, "agentURL", cfg.AgentURL, "static", staticDir)
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// staticDirIfPresent picks the SPA directory: STATIC_DIR env if set, else
// ./web/dist when it exists (local release build), else disabled (dev mode).
func staticDirIfPresent(log interface{ Info(string, ...any) }) string {
	if dir := os.Getenv("STATIC_DIR"); dir != "" {
		return dir
	}
	if _, err := os.Stat("web/dist/index.html"); err == nil {
		return "web/dist"
	}
	log.Info("no static dir found; serving API only (use Vite dev server for the UI)")
	return ""
}
