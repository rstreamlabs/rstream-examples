package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
)

func NewHandler(cfg config.Config, source sourceProvider, producer producerProvider) http.Handler {
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(cfg, source, producer))
	metricsHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.HTTPErrorOnError,
	})
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metricsHandler)
	mux.Handle("HEAD /metrics", metricsHandler)
	return mux
}
