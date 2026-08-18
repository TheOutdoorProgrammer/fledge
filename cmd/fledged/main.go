// Command fledged serves ad hoc iOS builds over the air.
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

	"github.com/theoutdoorprogrammer/fledge/internal/asc"
	"github.com/theoutdoorprogrammer/fledge/internal/config"
	"github.com/theoutdoorprogrammer/fledge/internal/httpapi"
	"github.com/theoutdoorprogrammer/fledge/internal/oidc"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
	"github.com/theoutdoorprogrammer/fledge/internal/version"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log); err != nil {
		log.Error("fledged", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}

	var apple *asc.Client
	if cfg.Apple.Enabled() {
		apple, err = asc.New(cfg.Apple.IssuerID, cfg.Apple.KeyID, cfg.Apple.PrivateKey)
		if err != nil {
			return err
		}
	}

	var workloads *oidc.Verifier
	if cfg.Workloads.Enabled() {
		policy, err := oidc.ParsePolicy(cfg.Workloads.Policy)
		if err != nil {
			return err
		}
		workloads, err = oidc.New(context.Background(), cfg.Workloads.Issuer, cfg.Workloads.Audience, policy)
		if err != nil {
			return err
		}
		log.Info("workload identity publishing enabled",
			"issuer", cfg.Workloads.Issuer, "audience", cfg.Workloads.Audience, "rules", len(policy))
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(cfg, st, httpapi.Options{Apple: apple, Workloads: workloads}, log),
		ReadHeaderTimeout: 10 * time.Second,
		// Uploads are whole application archives over a home network, so the
		// write timeout has to tolerate a slow client rather than a slow handler.
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	log.Info("fledged listening",
		"version", version.String(), "addr", cfg.Addr,
		"public_url", cfg.PublicURL, "data_dir", cfg.DataDir,
		"apple_registration", cfg.Apple.Enabled())

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
