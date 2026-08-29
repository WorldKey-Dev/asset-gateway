package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Config：环境变量契约。S3 段与 console 同契约（CONSOLE_S3_*），其余为网关自有。
type Config struct {
	Port         string
	DatabaseURL  string
	CacheControl string

	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func LoadConfig() Config {
	return Config{
		Port:         envOr("ASSET_GATEWAY_PORT", "8095"),
		DatabaseURL:  os.Getenv("CONSOLE_DATABASE_URL"),
		CacheControl: envOr("ASSET_GATEWAY_CACHE_CONTROL", "public, max-age=31536000, immutable"),
		S3Endpoint:   os.Getenv("CONSOLE_S3_ENDPOINT"),
		S3Bucket:     envOr("CONSOLE_S3_BUCKET", "console"),
		S3AccessKey:  os.Getenv("CONSOLE_S3_ACCESS_KEY"),
		S3SecretKey:  os.Getenv("CONSOLE_S3_SECRET_KEY"),
		S3Region:     envOr("CONSOLE_S3_REGION", "us-east-1"),
	}
}

func main() {
	cfg := LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.DatabaseURL == "" {
		log.Fatal("CONSOLE_DATABASE_URL is required")
	}
	store, err := NewPGStore(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("asset store: %v", err)
	}
	defer store.Close()

	objects, err := NewS3Source(ctx, cfg)
	if err != nil {
		log.Fatalf("object source: %v", err)
	}

	srv := NewServer(store, objects, cfg.CacheControl)
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("asset-gateway listening on :%s bucket=%s", cfg.Port, cfg.S3Bucket)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
