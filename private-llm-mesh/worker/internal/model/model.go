// Package model resolves a model reference to a local GGUF file, fetching it from
// Hugging Face on first use and caching it under the user cache directory, so the
// worker requires no pre-staged weights.
package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultRef is used when no model is requested.
const DefaultRef = "qwen3:4b"

type source struct{ repo, quant string }

// aliases map short names to a Hugging Face GGUF repository and quantization.
// The set is deliberately small: recognized, tool-capable instruct models that
// llama.cpp's common_chat can drive, spanning Alibaba (Qwen), OpenAI (gpt-oss),
// Meta (Llama), and European (Mistral) weights so the mesh is not tied to one
// vendor or region.
//
// Qwen3 models ending in -2507 are dedicated instruct builds: they answer
// directly. The other Qwen3 sizes are hybrid reasoning models that emit a
// <think> block first, which the app renders as reasoning but which costs
// noticeable latency on CPU. Prefer an instruct build for interactive chat and a
// reasoning build when answer quality matters more than time to first token.
var aliases = map[string]source{
	// Laptop-class: 4B–8B, runs on Apple Silicon or a modern CPU.
	"qwen3":    {"bartowski/Qwen_Qwen3-4B-Instruct-2507-GGUF", "Q4_K_M"},
	"qwen3:4b": {"bartowski/Qwen_Qwen3-4B-Instruct-2507-GGUF", "Q4_K_M"},
	"qwen3:8b": {"Qwen/Qwen3-8B-GGUF", "Q4_K_M"},
	"llama3.1": {"bartowski/Meta-Llama-3.1-8B-Instruct-GGUF", "Q4_K_M"},
	"llama3.2": {"bartowski/Llama-3.2-3B-Instruct-GGUF", "Q4_K_M"},

	// Homelab-class: 14B–30B, a Mac mini or small server with more memory.
	// `qwen3:30b` is a mixture-of-experts build: 30B of weights but only about
	// 3B active per token, so it runs far faster than its size suggests.
	"qwen3:14b":   {"Qwen/Qwen3-14B-GGUF", "Q4_K_M"},
	"qwen3:30b":   {"unsloth/Qwen3-30B-A3B-Instruct-2507-GGUF", "Q4_K_M"},
	"gpt-oss":     {"ggml-org/gpt-oss-20b-GGUF", "MXFP4"},
	"gpt-oss:20b": {"ggml-org/gpt-oss-20b-GGUF", "MXFP4"},
	"mistral":     {"bartowski/Mistral-Nemo-Instruct-2407-GGUF", "Q4_K_M"},

	// GPU-class: 32B and beyond, a card with ample VRAM.
	"qwen3:32b":    {"Qwen/Qwen3-32B-GGUF", "Q4_K_M"},
	"gpt-oss:120b": {"ggml-org/gpt-oss-120b-GGUF", "MXFP4"},
	"llama3.3":     {"bartowski/Llama-3.3-70B-Instruct-GGUF", "Q4_K_M"},

	// Previous generation, kept so existing deployments keep resolving. The
	// smallest sizes are also handy as fast fixtures for the engine tests.
	"qwen2.5":       {"Qwen/Qwen2.5-7B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:7b":    {"Qwen/Qwen2.5-7B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:3b":    {"Qwen/Qwen2.5-3B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:1.5b":  {"Qwen/Qwen2.5-1.5B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:0.5b":  {"Qwen/Qwen2.5-0.5B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:14b":   {"Qwen/Qwen2.5-14B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:32b":   {"Qwen/Qwen2.5-32B-Instruct-GGUF", "q4_k_m"},
	"qwen2.5:72b":   {"Qwen/Qwen2.5-72B-Instruct-GGUF", "q4_k_m"},
	"mistral:7b":    {"bartowski/Mistral-7B-Instruct-v0.3-GGUF", "Q4_K_M"},
	"mistral-nemo":  {"bartowski/Mistral-Nemo-Instruct-2407-GGUF", "Q4_K_M"},
	"mistral-small": {"bartowski/Mistral-Small-24B-Instruct-2501-GGUF", "Q4_K_M"},
}

// Resolved is a model ready to load.
type Resolved struct {
	Path string
	ID   string
}

// Resolve turns a reference — a local path, an alias (qwen2.5:3b), or a Hugging
// Face repository (owner/name[:quant]) — into a local GGUF path, downloading and
// caching every file (all shards of a split model) as needed.
func Resolve(ctx context.Context, reference string, logger *slog.Logger) (Resolved, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		reference = DefaultRef
	}
	if isFile(reference) {
		return Resolved{Path: reference, ID: idFromFile(reference)}, nil
	}
	src, id := parseRef(reference)
	files, err := pickGGUF(ctx, src)
	if err != nil {
		return Resolved{}, err
	}
	path, err := download(ctx, src.repo, files, logger)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Path: path, ID: id}, nil
}

func parseRef(reference string) (source, string) {
	if src, ok := aliases[strings.ToLower(reference)]; ok {
		return src, strings.ToLower(reference)
	}
	repo, quant, ok := strings.Cut(reference, ":")
	if !ok || quant == "" {
		quant = "q4_k_m"
	}
	return source{repo: repo, quant: quant}, repo[strings.LastIndex(repo, "/")+1:]
}

type hfModel struct {
	Siblings []struct {
		Rfilename string `json:"rfilename"`
	} `json:"siblings"`
}

// pickGGUF queries the Hugging Face API and returns every GGUF file for the
// requested quantization, sorted. A single-file model yields one entry; a split
// model yields all of its shards — llama.cpp loads the remaining shards from the
// same directory once the first is opened.
func pickGGUF(ctx context.Context, src source) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://huggingface.co/api/models/"+src.repo, nil)
	if err != nil {
		return nil, fmt.Errorf("create model query for %s: %w", src.repo, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query model %s: %w", src.repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query model %s: %s", src.repo, resp.Status)
	}
	var m hfModel
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode model %s: %w", src.repo, err)
	}
	quant := strings.ToLower(src.quant)
	var files []string
	for _, s := range m.Siblings {
		name := strings.ToLower(s.Rfilename)
		if strings.HasSuffix(name, ".gguf") && strings.Contains(name, quant) {
			files = append(files, s.Rfilename)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no %q GGUF found in %s", src.quant, src.repo)
	}
	sort.Strings(files)
	return files, nil
}

// download fetches every model file into the cache and returns the path of the
// first (the file handed to the engine). Files already present are reused.
func download(ctx context.Context, repo string, files []string, logger *slog.Logger) (string, error) {
	dir, err := cacheDir(repo)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	first := filepath.Join(dir, filepath.Base(files[0]))
	for i, file := range files {
		dest := filepath.Join(dir, filepath.Base(file))
		if fileReady(dest) {
			continue
		}
		if err := downloadFile(ctx, repo, file, dest, i+1, len(files), logger); err != nil {
			return "", err
		}
	}
	logger.Info("model ready", "path", first, "files", len(files))
	return first, nil
}

func downloadFile(ctx context.Context, repo, file, dest string, index, count int, logger *slog.Logger) error {
	if fileReady(dest) {
		return nil
	}
	url := "https://huggingface.co/" + repo + "/resolve/main/" + file
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request for %s: %w", file, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", file, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", file, resp.Status)
	}
	logger.Info("downloading model file",
		"file", file, "file_index", index, "file_count", count, "size_mb", resp.ContentLength>>20)
	out, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".partial-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	pr := &progressReader{r: resp.Body, total: resp.ContentLength, logger: logger, file: file, start: time.Now()}
	if _, err := io.Copy(out, pr); err != nil {
		out.Close()
		return fmt.Errorf("download %s: %w", file, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func fileReady(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

func cacheDir(repo string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "private-llm-mesh", "models", strings.ReplaceAll(repo, "/", "_")), nil
}

// progressReader reports download progress from the first 64 MB and at regular
// intervals, giving the fraction, size, and throughput.
type progressReader struct {
	r      io.Reader
	total  int64
	read   int64
	logged int64
	logger *slog.Logger
	file   string
	start  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	step := int64(64) << 20
	if p.total > 0 && p.total/50 < step {
		step = p.total / 50
	}
	if step > 0 && p.read-p.logged >= step {
		p.logged = p.read
		p.log()
	}
	return n, err
}

func (p *progressReader) log() {
	mb := p.read >> 20
	var mbPerSec int64
	if elapsed := time.Since(p.start).Seconds(); elapsed > 0 {
		mbPerSec = int64(float64(mb) / elapsed)
	}
	if p.total > 0 {
		p.logger.Info("downloading",
			"file", p.file, "pct", int(p.read*100/p.total),
			"mb", mb, "total_mb", p.total>>20, "mb_per_s", mbPerSec)
		return
	}
	p.logger.Info("downloading", "file", p.file, "mb", mb, "mb_per_s", mbPerSec)
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func idFromFile(p string) string {
	n := filepath.Base(p)
	return strings.TrimSuffix(n, filepath.Ext(n))
}
