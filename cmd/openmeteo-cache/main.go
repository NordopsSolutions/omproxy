package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"nordops/omproxy/internal/cache"
	"nordops/omproxy/internal/config"
	"nordops/omproxy/internal/httpapi"
	"nordops/omproxy/internal/openmeteo"
	"nordops/omproxy/internal/service"
	"nordops/omproxy/internal/stats"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolConfig, err := pgxpool.ParseConfig(cfg.Database.DSN())
	if err != nil {
		logger.Error("parse database dsn", "error", err)
		os.Exit(1)
	}
	poolConfig.MaxConns = cfg.Database.MaxOpenConns
	poolConfig.MinConns = 0
	poolConfig.MaxConnLifetime = cfg.Database.ConnMaxLifetime.Duration

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Error("ping database", "error", err)
		os.Exit(1)
	}
	if err := cache.InitSchema(ctx, pool); err != nil {
		logger.Error("init database schema", "error", err)
		os.Exit(1)
	}

	cacheStore := cache.New(pool)
	statsStore := stats.New(pool)
	openMeteoClient := openmeteo.New(cfg.OpenMeteo.BaseURL, cfg.OpenMeteo.RequestTimeout.Duration)
	weatherService := service.NewWeather(cfg, cacheStore, statsStore, openMeteoClient)
	api := httpapi.New(weatherService, statsStore, logger)

	server := &http.Server{
		Addr:         cfg.Server.ListenAddr,
		Handler:      api.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout.Duration,
		WriteTimeout: cfg.Server.WriteTimeout.Duration,
	}

	go func() {
		logger.Info("openmeteo cache listening", "addr", cfg.Server.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Duration)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("openmeteo cache stopped")
}
