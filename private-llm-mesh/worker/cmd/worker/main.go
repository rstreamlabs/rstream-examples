// Command worker serves a local GGUF model to the private-llm-mesh over an
// rstream tunnel, with robust text-or-tool tool-calling provided in-process by
// llama.cpp's common_chat. One binary, one process: the model runs here, there
// is no separate inference server to proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/app"
	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.FromArgs(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		logger.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("worker exited", "err", err)
		os.Exit(1)
	}
	logger.Info("worker stopped")
}
