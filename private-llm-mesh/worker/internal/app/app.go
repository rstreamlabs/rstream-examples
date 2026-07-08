// Package app wires the engine, the OpenAI HTTP layer, and the rstream tunnel
// into a supervised, reconnecting worker.
package app

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/config"
	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/engine"
	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/model"
	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/openai"
	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/tunnel"
)

// discoveryLabels are the tunnel labels the mesh app filters and reads on: the
// role/app pair it lists by, plus the model set and context it routes and budgets
// with. User --labels seed engine/accelerator; the contract keys are forced.
func discoveryLabels(cfg config.Config, modelID string) map[string]string {
	labels := map[string]string{"engine": "llama.cpp", "accelerator": "cpu"}
	for k, v := range cfg.Labels {
		labels[k] = v
	}
	labels["role"] = "llm"
	labels["app"] = "private-llm-mesh"
	labels["models"] = modelID
	labels["ctx"] = strconv.Itoa(cfg.NCtx)
	if _, set := labels["host"]; !set {
		labels["host"] = hostname()
	}
	return labels
}

// hostname is the machine's short name, or "unknown" if it cannot be read.
func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return strings.TrimSuffix(name, ".local")
}

const (
	minBackoff    = time.Second
	maxBackoff    = 30 * time.Second
	stableUptime  = 30 * time.Second
	shutdownGrace = 5 * time.Second
	readHeaderTO  = 10 * time.Second
	idleTO        = 2 * time.Minute
)

// Run resolves and loads the model once, then serves the OpenAI API over the
// rstream tunnel, reconnecting with jittered backoff until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	res, err := model.Resolve(ctx, cfg.Model, logger)
	if err != nil {
		return err
	}
	modelID := cfg.ModelID
	if modelID == "" {
		modelID = res.ID
	}
	logger.Info("loading model", "path", res.Path, "ctx", cfg.NCtx, "parallel", cfg.Parallel)
	eng, err := engine.Load(res.Path, cfg.NCtx, cfg.Parallel)
	if err != nil {
		return err
	}
	defer eng.Close()
	logger.Info("model loaded", "model", modelID, "parallel", cfg.Parallel)
	cfg.Labels = discoveryLabels(cfg, modelID)
	handler := openai.NewServer(eng, modelID, cfg.MaxTokens, cfg.Temp, cfg.MaxGenTime, logger).Handler()
	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		online, err := serveOnce(ctx, cfg, handler, logger)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if online >= stableUptime {
			backoff = minBackoff
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		logger.Warn("tunnel dropped; reconnecting", "err", err, "in", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// serveOnce opens the tunnel and serves until it drops or ctx is cancelled,
// returning how long the worker was online.
func serveOnce(ctx context.Context, cfg config.Config, handler http.Handler, logger *slog.Logger) (time.Duration, error) {
	mgr, err := tunnel.Open(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer mgr.Close()
	logger.Info("worker online", "url", mgr.PublicURL(), "tunnel", cfg.TunnelName)
	start := time.Now()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTO, IdleTimeout: idleTO}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(mgr.Listener())
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return time.Since(start), err
}
