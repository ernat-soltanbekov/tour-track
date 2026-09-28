package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ernat-soltanbekov/tour-track/internal/catalog"
	"github.com/ernat-soltanbekov/tour-track/internal/web"
)

type config struct{ address, apiURL string }

func readConfig(getenv func(string) string) (config, error) {
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return config{}, fmt.Errorf("PORT must be an integer from 1 to 65535")
	}
	host := getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	base := getenv("API_BASE_URL")
	if base == "" {
		base = "https://groupietrackers.herokuapp.com/api"
	}
	return config{address: net.JoinHostPort(host, port), apiURL: base}, nil
}

func run(ctx context.Context, c config) error {
	client, err := catalog.NewClient(c.apiURL, 8*time.Second)
	if err != nil {
		return err
	}
	cache, err := catalog.NewCache(ctx, client, catalog.DefaultCacheOptions())
	if err != nil {
		return err
	}
	handler, err := web.New(cache)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", c.address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", c.address, err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	slog.Info("tour-track ready", "address", listener.Addr().String())
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

func main() {
	c, err := readConfig(os.Getenv)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
