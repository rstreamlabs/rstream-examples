// See LICENSE file in the project root for license information.

package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type CounterSet struct {
	mu       sync.Mutex
	counters map[string]uint64
}

func NewCounterSet() *CounterSet {
	return &CounterSet{
		counters: make(map[string]uint64),
	}
}

func (m *CounterSet) Inc(name string, labels map[string]string) {
	m.Add(name, labels, 1)
}

func (m *CounterSet) Add(name string, labels map[string]string, value uint64) {
	if m == nil {
		return
	}
	key := metricKey(name, labels)
	m.mu.Lock()
	m.counters[key] += value
	m.mu.Unlock()
}

func (m *CounterSet) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain; version=0.0.4; charset=utf-8")
		m.WritePrometheus(w)
	})
}

func (m *CounterSet) WritePrometheus(w io.Writer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	snapshot := make(map[string]uint64, len(m.counters))
	for key, value := range m.counters {
		snapshot[key] = value
	}
	m.mu.Unlock()
	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "%s %d\n", key, snapshot[key])
	}
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return sanitizeMetricName(name)
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(sanitizeMetricName(name))
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(sanitizeLabelName(key))
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(labels[key]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func sanitizeMetricName(value string) string {
	if value == "" {
		return "rstream_private_masque_unknown"
	}
	return sanitize(value)
}

func sanitizeLabelName(value string) string {
	if value == "" {
		return "label"
	}
	return sanitize(value)
}

func sanitize(value string) string {
	var b strings.Builder
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
