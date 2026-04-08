package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/app"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "configuration file path")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}
	instance, err := app.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer func() {
		signal.Stop(signals)
		cancel()
	}()
	go func() {
		<-signals
		cancel()
		<-signals
		fmt.Fprintln(os.Stderr, "forced shutdown")
		os.Exit(1)
	}()
	if err := instance.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "runtime error: %v\n", err)
		os.Exit(1)
	}
}
