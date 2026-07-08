// Package config parses the worker's flags and environment into a flat Config.
package config

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved worker configuration.
type Config struct {
	Model      string
	ModelID    string
	NCtx       int
	MaxTokens  int
	Temp       float32
	Parallel   int
	MaxGenTime time.Duration
	TunnelName string
	Labels     map[string]string
	TokenAuth  bool
	Engine     string
	Token      string
}

// FromArgs resolves configuration from CLI args, falling back to env vars.
func FromArgs(args []string) (Config, error) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	model := fs.String("model", env("PLLM_MODEL", ""), "model: local GGUF path, alias (qwen2.5:3b), or HF repo owner/name[:quant] (default qwen2.5:7b)")
	modelID := fs.String("model-id", env("PLLM_MODEL_ID", ""), "model id on /v1/models (default: derived from the model)")
	nCtx := fs.Int("ctx", envInt("PLLM_CTX", 8192), "context window size")
	maxTokens := fs.Int("max-tokens", envInt("PLLM_MAX_TOKENS", 0), "default max tokens per response (0 = until EOS or the context limit)")
	temp := fs.Float64("temp", envFloat("PLLM_TEMP", 0), "default sampling temperature (0 = greedy)")
	parallel := fs.Int("parallel", envInt("PLLM_PARALLEL", 1), "concurrent decoding contexts (each holds its own KV cache)")
	maxGenTime := fs.Duration("max-gen-time", envDuration("PLLM_MAX_GEN_TIME", 5*time.Minute), "max wall-clock time per response")
	tunnel := fs.String("tunnel-name", env("PLLM_TUNNEL_NAME", "private-llm-mesh"), "published tunnel name")
	labels := fs.String("labels", env("PLLM_LABELS", ""), "comma-separated key=value tunnel labels")
	tokenAuth := fs.Bool("token-auth", envBool("PLLM_TOKEN_AUTH", true), "require token auth on the tunnel")
	engine := fs.String("rstream-engine", env("PLLM_RSTREAM_ENGINE", ""), "provisioned rstream engine URL (optional)")
	token := fs.String("rstream-token", env("PLLM_RSTREAM_TOKEN", ""), "provisioned rstream token (optional)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return Config{
		Model:      strings.TrimSpace(*model),
		ModelID:    strings.TrimSpace(*modelID),
		NCtx:       *nCtx,
		MaxTokens:  *maxTokens,
		Temp:       float32(*temp),
		Parallel:   *parallel,
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

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}
