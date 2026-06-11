package main

import (
	"context"
	"fmt"
	"os"

	"github.com/brassinai/inferops/operator/internal/scheduler"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "operator failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	_ = scheduler.NewPlanner()
	fmt.Println("InferOps operator bootstrap")
	return nil
}
