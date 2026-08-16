// Package config parses the worker's flags and environment into a flat Config.
package config

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxContextTokens = 1 << 20
	maxParallel      = 1024
	maxQueueLimit    = 1 << 16
	maxTokensLimit   = 1 << 20
)

// Config is the fully-resolved worker configuration.
type Config struct {
	Model      string
	ModelID    string
	NCtx       int
	MaxTokens  int
	Temp       float32
	Parallel   int
	MaxQueue   int
	MaxGenTime time.Duration
	TunnelName string
	Labels     map[string]string
	TokenAuth  bool
	Engine     string
	Token      string
}

// FromArgs resolves configuration from CLI args, falling back to env vars.
func FromArgs(args []string) (Config, error) {
	nCtxDefault, err := envInt("PLLM_CTX", 8192)
	if err != nil {
		return Config{}, err
	}
	maxTokensDefault, err := envInt("PLLM_MAX_TOKENS", 0)
	if err != nil {
		return Config{}, err
	}
	tempDefault, err := envFloat("PLLM_TEMP", 0)
	if err != nil {
		return Config{}, err
	}
	parallelDefault, err := envInt("PLLM_PARALLEL", 1)
	if err != nil {
		return Config{}, err
	}
	maxQueueDefault, err := envInt("PLLM_MAX_QUEUE", 4)
	if err != nil {
		return Config{}, err
	}
	maxGenTimeDefault, err := envDuration("PLLM_MAX_GEN_TIME", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	tokenAuthDefault, err := envBool("PLLM_TOKEN_AUTH", true)
	if err != nil {
		return Config{}, err
	}
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	model := fs.String("model", env("PLLM_MODEL", ""), "model: local GGUF path, alias (qwen2.5:3b), or HF repo owner/name[:quant] (default qwen2.5:7b)")
	modelID := fs.String("model-id", env("PLLM_MODEL_ID", ""), "model id on /v1/models (default: derived from the model)")
	nCtx := fs.Int("ctx", nCtxDefault, "context window size")
	maxTokens := fs.Int("max-tokens", maxTokensDefault, "default max tokens per response (0 = until EOS or the context limit)")
	temp := fs.Float64("temp", tempDefault, "default sampling temperature (0 = greedy)")
	parallel := fs.Int("parallel", parallelDefault, "concurrent decoding contexts (each holds its own KV cache)")
	maxQueue := fs.Int("max-queue", maxQueueDefault, "maximum requests waiting for a decoding context")
	maxGenTime := fs.Duration("max-gen-time", maxGenTimeDefault, "max wall-clock time per response")
	tunnel := fs.String("tunnel-name", env("PLLM_TUNNEL_NAME", "private-llm-mesh"), "published tunnel name")
	labels := fs.String("labels", env("PLLM_LABELS", ""), "comma-separated key=value tunnel labels")
	tokenAuth := fs.Bool("token-auth", tokenAuthDefault, "require token auth on the tunnel")
	engine := fs.String("rstream-engine", env("PLLM_RSTREAM_ENGINE", ""), "provisioned rstream engine URL (optional)")
	token := fs.String("rstream-token", env("PLLM_RSTREAM_TOKEN", ""), "provisioned rstream token (optional)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	invalid := *nCtx < 1 ||
		*nCtx > maxContextTokens ||
		*parallel < 1 ||
		*parallel > maxParallel ||
		*maxQueue < 0 ||
		*maxQueue > maxQueueLimit ||
		*maxTokens < 0 ||
		*maxTokens > maxTokensLimit ||
		*maxGenTime <= 0 ||
		math.IsNaN(*temp) ||
		math.IsInf(*temp, 0) ||
		*temp < 0 ||
		*temp > 2
	if invalid {
		return Config{}, fmt.Errorf(
			"ctx must be in [1,%d], parallel in [1,%d], max-queue in [0,%d], max-tokens in [0,%d], max-gen-time positive, and temp in [0,2]",
			maxContextTokens,
			maxParallel,
			maxQueueLimit,
			maxTokensLimit,
		)
	}
	return Config{
		Model:      strings.TrimSpace(*model),
		ModelID:    strings.TrimSpace(*modelID),
		NCtx:       *nCtx,
		MaxTokens:  *maxTokens,
		Temp:       float32(*temp),
		Parallel:   *parallel,
		MaxQueue:   *maxQueue,
		MaxGenTime: *maxGenTime,
		TunnelName: strings.TrimSpace(*tunnel),
		Labels:     parseLabels(*labels),
		TokenAuth:  *tokenAuth,
		Engine:     strings.TrimSpace(*engine),
		Token:      strings.TrimSpace(*token),
	}, nil
}

func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func envFloat(key string, def float64) (float64, error) {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	if v, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("%s must be a duration: %w", key, err)
		}
		return d, nil
	}
	return def, nil
}

func envBool(key string, def bool) (bool, error) {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, fmt.Errorf("%s must be a boolean: %w", key, err)
		}
		return b, nil
	}
	return def, nil
}
