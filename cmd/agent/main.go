// Command agent runs the AI agent service: the periodic cluster review loop
// and the on-demand query API. It is deployed separately from the UI backend
// and only needs read access to the cluster.
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

	"github.com/emreoztoprak/kentinel/internal/agent"
	"github.com/emreoztoprak/kentinel/internal/config"
	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAgent()
	if err != nil {
		return err
	}
	log := logging.New(cfg.LogLevel, cfg.LogFormat)

	client, err := k8s.NewClient(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("connecting to kubernetes: %w (is a kubeconfig available, or are we running in-cluster?)", err)
	}

	var store *agent.Store
	if cfg.InsightDBPath != "" {
		store = agent.NewPersistentStore(cfg.InsightDBPath, cfg.InsightRetentionDays, 20, log)
	} else {
		store = agent.NewStore(20)
		log.Info("insight history and settings are in-memory only (set INSIGHT_DB_PATH to persist across restarts)")
	}

	runtime, err := agent.NewRuntime(cfg, store, log)
	if err != nil {
		return fmt.Errorf("configuring LLM provider: %w", err)
	}
	log.Info("llm provider configured", "provider", runtime.Provider().Name(), "model", runtime.Provider().Model())

	queryEngine := agent.NewQueryEngine(client, runtime, store, log)
	notifier := agent.NewDispatcher(runtime, log)
	reporter := agent.NewReporter(store, runtime, notifier, log)
	api := agent.NewAPI(store, queryEngine, runtime, notifier, reporter, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The monitor always runs; enablement and interval are runtime settings
	// it re-reads every iteration (configurable from the UI).
	monitor := agent.NewMonitor(client, runtime, store, notifier, log)
	go monitor.Run(ctx)
	// Same pattern for the daily report scheduler.
	go reporter.Run(ctx)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           api.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("agent listening", "port", cfg.Port)
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
