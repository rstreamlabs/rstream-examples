// See LICENSE file in the project root for license information.

package metrics

import (
	"bytes"
	"testing"
)

func TestCounterSetWritesPrometheus(t *testing.T) {
	counters := NewCounterSet()
	counters.Inc("rstream_private_masque_requests_total", map[string]string{"protocol": "connect", "outcome": "ok"})
	counters.Add("rstream_private_masque_bytes_total", map[string]string{"protocol": "connect", "direction": "upstream"}, 42)
	var out bytes.Buffer
	counters.WritePrometheus(&out)
	if !bytes.Contains(out.Bytes(), []byte(`rstream_private_masque_requests_total{outcome="ok",protocol="connect"} 1`)) {
		t.Fatalf("missing requests counter:\n%s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`rstream_private_masque_bytes_total{direction="upstream",protocol="connect"} 42`)) {
		t.Fatalf("missing bytes counter:\n%s", out.String())
	}
}
