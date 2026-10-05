package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"loyaltyledger/internal/config"
	"loyaltyledger/internal/handler"
	"loyaltyledger/internal/orders"
	"loyaltyledger/internal/router"
	"loyaltyledger/internal/service"
	"loyaltyledger/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()
	repo, err := store.Open(initCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	if err = repo.Migrate(initCtx); err != nil {
		return err
	}
	external := orders.New(cfg.OrderServiceURL, cfg.OrderServiceAPIKey, time.Duration(cfg.OrderServiceTimeoutSeconds)*time.Second)
	svc := service.New(repo, external, service.Options{Interval: cfg.Interval(), Concurrency: cfg.WorkerConcurrency,
		RequestTimeout: time.Duration(cfg.OrderServiceTimeoutSeconds+5) * time.Second, TokenTTL: time.Duration(cfg.TokenTTLHours) * time.Hour, RewardPercent: cfg.RewardPercent})
	if err = svc.EnsureAdmin(initCtx, cfg.AdminLogin, cfg.AdminPassword); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); svc.StartAccrualWorker(workerCtx) }()
	srv := &http.Server{Addr: cfg.Address(), Handler: router.New(handler.New(svc), svc, repo.Ping, strings.EqualFold(cfg.LogLevel, "debug")),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	failures := make(chan error, 1)
	go func() { log.Printf("LoyaltyLedger listening on %s", cfg.Address()); failures <- srv.ListenAndServe() }()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failures:
	}
	cancelWorker()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		log.Printf("shutdown: %v", err)
	}
	<-workerDone
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
