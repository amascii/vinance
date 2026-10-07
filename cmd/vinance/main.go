package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amascii/vinance/internal/config"
	"github.com/amascii/vinance/internal/db"
	"github.com/amascii/vinance/internal/logging"
	"github.com/amascii/vinance/internal/web"
)

func main() {
	cfg := config.Load()
	logger := logging.New(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error
	switch cmd {
	case "serve":
		err = serve(cfg, logger)
	case "migrate":
		err = migrate(cfg, logger)
	default:
		err = errors.New("unknown command: " + cmd)
	}
	if err != nil {
		logger.Error("command failed", "command", cmd, "error", err)
		os.Exit(1)
	}
}

// openDB opens the database and applies pending migrations.
func openDB(ctx context.Context, cfg config.Config, logger *slog.Logger) (*sql.DB, error) {
	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, conn, logger); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func migrate(cfg config.Config, logger *slog.Logger) error {
	conn, err := openDB(context.Background(), cfg, logger)
	if err != nil {
		return err
	}
	defer conn.Close()
	logger.Info("migrations up to date", "db", cfg.DBPath)
	return nil
}

func serve(cfg config.Config, logger *slog.Logger) error {
	conn, err := openDB(context.Background(), cfg, logger)
	if err != nil {
		return err
	}
	defer conn.Close()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           web.NewRouter(logger, conn),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
