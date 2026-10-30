package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "validate-config" {
		cfg, err := loadConfig()
		if err != nil {
			log.Fatal(err)
		}
		if err := validateConfig(cfg); err != nil {
			log.Fatal(err)
		}
		log.Println(safeConfigSummary(cfg))
		warnIneligibleTiers(cfg)
		warnNoStateFile(cfg)
		warnNoUsageFile(cfg)
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	warnIneligibleTiers(cfg)
	warnNoStateFile(cfg)
	warnNoUsageFile(cfg)
	a := newApp(cfg)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: a, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 65 * time.Second, WriteTimeout: 0, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		a.history.close()
	}()
	log.Printf("startup listen_addr=%s upstream_base_url=%s upstream_keys=%d smtp_configured=%t config_source=%s max_request_body_bytes=%d retry_exhausted_after=%s response_header_timeout=%s", cfg.ListenAddr, cfg.UpstreamBaseURL, len(cfg.UpstreamAPIKeys), cfg.SMTP.Host != "" && cfg.SMTP.From != "" && cfg.SMTP.To != "", defaultString(cfg.ConfigSourcePath, "none"), cfg.MaxRequestBodyBytes, cfg.RetryExhaustedAfter, cfg.UpstreamResponseHeaderTimeout)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-done
}
