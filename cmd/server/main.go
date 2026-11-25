// Command server runs the Travel Next web app, its API and the itinerary
// generator.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jamesh/travel-next/internal/ai"
	"github.com/jamesh/travel-next/internal/auth"
	"github.com/jamesh/travel-next/internal/config"
	"github.com/jamesh/travel-next/internal/promptctx"
	"github.com/jamesh/travel-next/internal/server"
	"github.com/jamesh/travel-next/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	level := slog.LevelInfo
	if cfg.Env == "development" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Database: wait for it, then migrate. Both are safe to repeat, which is
	// what makes a rolling restart in Kubernetes uneventful.
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.WaitForDB(ctx, 60*time.Second); err != nil {
		return err
	}
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("database ready")

	prompt, err := promptctx.NewLoader(cfg.PromptContextFile)
	if err != nil {
		return err
	}
	pc := prompt.Current()
	log.Info("prompt context loaded", "source", pc.Source, "checksum", pc.Checksum, "version", pc.Version)

	gen, err := ai.New(cfg.AI, prompt)
	if err != nil {
		return err
	}
	log.Info("itinerary generator ready", "generator", gen.Name())

	authSvc := auth.NewService(st, cfg.SessionTTL, cfg.CookieSecure)
	srv, err := server.New(cfg, st, authSvc, gen, prompt, log)
	if err != nil {
		return err
	}

	go reapSessions(ctx, st, log)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "base_url", cfg.BaseURL, "env", cfg.Env)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Drain in-flight requests; background generations finish on their own
	// timeout, so give them a little room before the process exits.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func reapSessions(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := st.DeleteExpiredSessions(ctx)
			if err != nil {
				log.Warn("session cleanup failed", "error", err)
				continue
			}
			if n > 0 {
				log.Info("expired sessions removed", "count", n)
			}
		}
	}
}
