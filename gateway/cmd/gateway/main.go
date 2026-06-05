package main

import (
	"context"
	"fmt"
	"os"

	"github.com/brassinai/inferops/gateway/internal/routing"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "gateway failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	_ = routing.NewRouter()
	fmt.Println("nano-vLLM gateway bootstrap")
	return nil
}
