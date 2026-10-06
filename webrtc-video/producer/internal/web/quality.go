package web

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/adaptation"
)

// The local listener has no rstream edge authentication. Require a loopback Host
// for control requests so a rebinding origin cannot mutate a local producer.
func LocalHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/quality" {
			host := r.Host
			if hostname, _, err := net.SplitHostPort(host); err == nil {
				host = hostname
			}
			ip := net.ParseIP(strings.Trim(host, "[]"))
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				http.Error(w, "local quality control requires a loopback host", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleQuality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.qualityState == nil || s.selectQuality == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "cross-origin quality control is not allowed", http.StatusForbidden)
		return
	}
	if _, err := s.qualityState(); errors.Is(err, adaptation.ErrQualityDisabled) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	var state adaptation.QualityState
	var err error
	if r.Method == http.MethodGet {
		state, err = s.qualityState()
	} else {
		if encoding := r.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
			http.Error(w, "unsupported content encoding", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		defer r.Body.Close()
		var request struct {
			Mode    string `json:"mode"`
			Version string `json:"version"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&request); decodeErr != nil {
			http.Error(w, "invalid quality request", http.StatusBadRequest)
			return
		}
		if decodeErr := decoder.Decode(new(any)); decodeErr != io.EOF || len(request.Mode) > 32 || len(request.Version) > 64 {
			http.Error(w, "invalid quality request", http.StatusBadRequest)
			return
		}
		state, err = s.selectQuality(r.Context(), request.Mode, request.Version)
	}
	if err != nil {
		switch {
		case errors.Is(err, adaptation.ErrQualityDisabled):
			http.NotFound(w, r)
		case errors.Is(err, adaptation.ErrQualityVersion):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, adaptation.ErrQualityMode):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, "quality control unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
