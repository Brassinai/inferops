// Package health provides the minimal health server shared by InferOps processes.
package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	shutdownTimeout = 10 * time.Second
	idleTimeout     = 60 * time.Second
)

// Run serves liveness and readiness endpoints until the context is canceled.
func Run(ctx context.Context, address string) error {
	if address == "" {
		return errors.New("health server address is required")
	}

	server := &http.Server{
		Addr:              address,
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       idleTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve health endpoints: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down health server: %w", err)
		}
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve health endpoints: %w", err)
		}
		return nil
	}
}

// Handler returns the process health endpoints.
func Handler() http.Handler {
	mux := http.NewServeMux()
	healthy := func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("ok\n"))
	}
	mux.HandleFunc("GET /healthz", healthy)
	mux.HandleFunc("GET /readyz", healthy)
	return mux
}
