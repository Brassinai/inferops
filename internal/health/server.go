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
	return RunWithHandler(ctx, address, Handler())
}

// RunWithHandler serves handler until the context is canceled.
func RunWithHandler(ctx context.Context, address string, handler http.Handler) error {
	if address == "" {
		return errors.New("health server address is required")
	}
	if handler == nil {
		return errors.New("HTTP handler is required")
	}

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
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
	return HandlerWithReadiness(nil)
}

// HandlerWithReadiness returns process health endpoints and evaluates ready for
// every readiness request. A nil check preserves the always-ready behavior used
// by processes without an external synchronization dependency.
func HandlerWithReadiness(ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if ready != nil && !ready() {
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = response.Write([]byte("not ready\n"))
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("ok\n"))
	})
	return mux
}
