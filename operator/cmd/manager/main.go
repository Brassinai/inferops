package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/brassinai/inferops/internal/health"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "operator failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return health.Run(ctx, healthAddress())
}

func healthAddress() string {
	if address := os.Getenv("INFEROPS_HEALTH_ADDR"); address != "" {
		return address
	}
	return ":8081"
}
