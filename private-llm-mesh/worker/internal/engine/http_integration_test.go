package engine_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/engine"
	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/openai"
)

// Isolates the HTTP+engine concurrency path from the rstream tunnel: serves the
// real handler over local HTTP and fires concurrent requests.
func TestConcurrentHTTP(t *testing.T) {
	model := os.Getenv("MODEL")
	if model == "" {
		t.Skip("set MODEL=/path/to/gguf to run engine tests")
	}
	e, err := engine.Load(model, 4096, 2)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer e.Close()
	srv := httptest.NewServer(openai.NewServer(e, "test", 64, 0, time.Minute, nil).Handler())
	defer srv.Close()
	const n = 3
	body := `{"model":"x","messages":[{"role":"user","content":"In one word, name a color"}],"max_tokens":16}`
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				errs[i] = err
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent HTTP requests did not complete in 30s (deadlock?)")
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
}
