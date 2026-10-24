package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"ymca/internal/app"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	cmd := app.New(os.Stdout, os.Stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", app.SafeError(err))
		os.Exit(app.ExitCode(err))
	}
}
