package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/webrtc"
)

type staticSourceProvider struct {
	stats media.SourceStats
}

type staticProducerProvider struct {
	stats rtc.ProducerStats
}

func (p staticSourceProvider) StatsSnapshot() media.SourceStats {
	return p.stats
}

func (p staticProducerProvider) MetricsSnapshot() rtc.ProducerStats {
	return p.stats
}

func TestCollectorExportsStableUnitsAndBoundedDimensions(t *testing.T) {
	cfg := config.Default()
	cfg.WebRTC.Adaptive.Enabled = true
	cfg.Media.Mode = config.MediaModePerViewer
	source := staticSourceProvider{stats: media.SourceStats{
		Sources:               1,
		EncodedBytes:          8000,
		EncodedFrames:         30,
		EncodedKeyFrames:      1,
		DeliveryDroppedFrames: 2,
	}}
	producer := staticProducerProvider{stats: rtc.ProducerStats{
		ActiveSessions:              1,
		EstimatedBitrateBps:         8_000_000,
		MaximumPacketLossRatio:      0.025,
		MaximumDelayEstimateSeconds: 0.018,
		PacerSentPrimaryBytes:       9000,
		TWCCFeedbackPackets:         10,
		TWCCReportedStatuses:        100,
		TWCCReportedLost:            3,
	}}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(cfg, source, producer))
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	if value := metricValue(t, families, namespace+"_twcc_estimated_available_bytes_per_second", nil); value != 1_000_000 {
		t.Fatalf("estimated available throughput = %f bytes/s, want 1000000", value)
	}
	if value := metricValue(t, families, namespace+"_twcc_maximum_packet_loss_ratio", nil); value != 0.025 {
		t.Fatalf("packet loss ratio = %f, want 0.025", value)
	}
	if value := metricValue(t, families, namespace+"_encoded_frames_total", map[string]string{"frame_type": "delta"}); value != 29 {
		t.Fatalf("encoded delta frames = %f, want 29", value)
	}
	if value := metricValue(t, families, namespace+"_feature_enabled", map[string]string{"feature": "adaptive_bitrate"}); value != 1 {
		t.Fatalf("adaptive bitrate feature = %f, want 1", value)
	}
	if value := metricValue(t, families, namespace+"_pacer_sent_bytes_total", map[string]string{"kind": "primary"}); value != 9000 {
		t.Fatalf("primary wire bytes = %f, want 9000", value)
	}
	if unit := metricUnit(t, families, namespace+"_twcc_estimated_available_bytes_per_second"); unit != "bytes_per_second" {
		t.Fatalf("estimated throughput unit = %q, want bytes_per_second", unit)
	}
	if unit := metricUnit(t, families, namespace+"_pacer_sent_bytes_total"); unit != "bytes" {
		t.Fatalf("wire byte unit = %q, want bytes", unit)
	}
}

func TestHandlerNegotiatesOpenMetricsAndSupportsConcurrentScrapes(t *testing.T) {
	handler := NewHandler(
		config.Default(),
		staticSourceProvider{},
		staticProducerProvider{},
	)
	const scrapers = 32
	start := make(chan struct{})
	var scrapersDone sync.WaitGroup
	scrapersDone.Add(scrapers)
	for range scrapers {
		go func() {
			defer scrapersDone.Done()
			<-start
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			request.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			result := response.Result()
			defer func() { _ = result.Body.Close() }()
			body, err := io.ReadAll(result.Body)
			if err != nil {
				t.Errorf("read metrics response: %v", err)
				return
			}
			if result.StatusCode != http.StatusOK {
				t.Errorf("metrics status = %d, want 200", result.StatusCode)
				return
			}
			if !strings.HasPrefix(result.Header.Get("Content-Type"), "application/openmetrics-text") {
				t.Errorf("metrics content type = %q", result.Header.Get("Content-Type"))
			}
			if !strings.HasSuffix(string(body), "# EOF\n") {
				t.Errorf("OpenMetrics response is missing the EOF marker")
			}
			if !strings.Contains(string(body), "# UNIT "+namespace+"_encoded_bytes bytes") {
				t.Errorf("OpenMetrics response is missing unit metadata")
			}
		}()
	}
	close(start)
	scrapersDone.Wait()
}

func TestHandlerDoesNotExposeMetricsOnOtherPaths(t *testing.T) {
	handler := NewHandler(
		config.Default(),
		staticSourceProvider{},
		staticProducerProvider{},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("root status = %d, want 404", response.Code)
	}
}

func metricValue(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) float64 {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if !metricHasLabels(metric, labels) {
				continue
			}
			if metric.Counter != nil {
				return metric.Counter.GetValue()
			}
			if metric.Gauge != nil {
				return metric.Gauge.GetValue()
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return 0
}

func metricHasLabels(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.Label) != len(labels) {
		return false
	}
	for _, pair := range metric.Label {
		if labels[pair.GetName()] != pair.GetValue() {
			return false
		}
	}
	return true
}

func metricUnit(t *testing.T, families []*dto.MetricFamily, name string) string {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family.GetUnit()
		}
	}
	t.Fatalf("metric family %s not found", name)
	return ""
}
