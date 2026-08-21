package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/webrtc"
	"github.com/rstreamlabs/rstream-go"
)

type Info struct {
	LocalURL        string                  `json:"localURL"`
	PublicURL       *string                 `json:"publicURL,omitempty"`
	TunnelAuth      config.TunnelAuthConfig `json:"tunnelAuth"`
	VideoMimeType   string                  `json:"videoMimeType"`
	TWCCEnabled     bool                    `json:"twccEnabled"`
	NACKEnabled     bool                    `json:"nackEnabled"`
	RTXEnabled      bool                    `json:"rtxEnabled"`
	FlexFECEnabled  bool                    `json:"flexFECEnabled"`
	AdaptiveBackend config.AdaptiveBackend  `json:"adaptiveBackend"`
}

type Session interface {
	ID() string
	Done() <-chan struct{}
	HandleWHEPOffer(context.Context, string) (string, error)
	RefreshWHEPICE(context.Context) error
	HandleWHEPICE(context.Context, string, bool) (string, error)
	StatsSnapshot() rtc.SessionStats
	Close(string)
}

type Server struct {
	logger      *logs.Logger
	createTURN  func(context.Context) (*rstream.TURNCredentials, error)
	openSession func(context.Context) (Session, error)
	whep        *whepServer
	viewer      bool
	mu          sync.RWMutex
	info        Info
}

type ServerOptions struct {
	Viewer bool
}

type turnCredentialsResponse struct {
	*rstream.TURNCredentials
	ExpiresAt time.Time `json:"expiresAt"`
}

func NewServer(
	logger *logs.Logger,
	createTURN func(context.Context) (*rstream.TURNCredentials, error),
	openSession func(context.Context) (Session, error),
	options ...ServerOptions,
) *Server {
	viewer := true
	if len(options) > 0 {
		viewer = options[0].Viewer
	}
	checkOrigin := sameOrigin
	if !viewer {
		checkOrigin = browserOrigin
	}
	server := &Server{
		logger:      logger,
		createTURN:  createTURN,
		openSession: openSession,
		viewer:      viewer,
	}
	server.whep = newWHEPServer(logger, openSession, checkOrigin)
	return server
}

func (s *Server) SetInfo(info Info) {
	s.mu.Lock()
	s.info = info
	s.mu.Unlock()
}

func (s *Server) WHEPInitialRequests() map[string]uint64 {
	return s.whep.initialRequestSnapshot()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.mountViewerRoutes(mux)
	s.mountAPIRoutes(mux)
	s.mountRealtimeRoutes(mux)
	s.mountHealthRoutes(mux)
	return mux
}

func (s *Server) mountViewerRoutes(mux *http.ServeMux) {
	if s.viewer {
		mux.HandleFunc("GET /{$}", s.handleIndex)
		mux.HandleFunc("GET /app.js", s.handleStatic)
		mux.HandleFunc("GET /app.css", s.handleStatic)
		mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	}
}

func (s *Server) mountAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /api/turn", s.handleAPITURN)
	mux.HandleFunc("GET /api/diagnostics/sessions/{session}", s.handleAPISessionDiagnostics)
}

func (s *Server) mountRealtimeRoutes(mux *http.ServeMux) {
	mux.Handle("HEAD /whep", s.whep)
	mux.Handle("GET /whep", s.whep)
	mux.Handle("OPTIONS /whep", s.whep)
	mux.Handle("POST /whep", s.whep)
	mux.Handle("GET /whep/{session}", s.whep)
	mux.Handle("OPTIONS /whep/{session}", s.whep)
	mux.Handle("PATCH /whep/{session}", s.whep)
	mux.Handle("DELETE /whep/{session}", s.whep)
}

func (s *Server) mountHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealth)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	s.serveAsset(w, "embed/index.html", "text/html; charset=utf-8")
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	switch path.Base(r.URL.Path) {
	case "app.js":
		s.serveAsset(w, "generated/app.js", "application/javascript; charset=utf-8")
	case "app.css":
		s.serveAsset(w, "generated/app.css", "text/css; charset=utf-8")
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleFavicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	info := s.info
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAPITURN(w http.ResponseWriter, r *http.Request) {
	issuedAt := time.Now()
	credentials, err := s.createTURN(r.Context())
	if err != nil {
		s.logger.Error("TURN credential request failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to issue TURN credentials",
		})
		return
	}
	writeJSON(w, http.StatusOK, turnCredentialsResponse{TURNCredentials: credentials, ExpiresAt: issuedAt.Add(time.Duration(credentials.TTL) * time.Second)})
}

func (s *Server) handleAPISessionDiagnostics(w http.ResponseWriter, r *http.Request) {
	stats, status := s.whep.diagnostics(strings.TrimSpace(r.PathValue("session")), bearerToken(r.Header.Get("Authorization")))
	if status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) serveAsset(w http.ResponseWriter, name, contentType string) {
	body, err := readEmbeddedAsset(name)
	if err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "failed to encode JSON response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return sameOriginHost(parsed.Host, r.Host, parsed.Scheme)
}

func browserOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func sameOriginHost(originHost string, requestHost string, scheme string) bool {
	normalizedOriginHost, ok := normalizeOriginHost(originHost, scheme)
	if !ok {
		return false
	}
	normalizedRequestHost, ok := normalizeOriginHost(requestHost, scheme)
	if !ok {
		return false
	}
	return normalizedOriginHost == normalizedRequestHost
}

func normalizeOriginHost(host string, scheme string) (string, bool) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", false
	}
	hostname := host
	port := ""
	if strings.Contains(host, ":") {
		splitHost, splitPort, err := net.SplitHostPort(host)
		if err == nil {
			hostname = splitHost
			port = splitPort
		} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			hostname = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		} else {
			return "", false
		}
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return "", false
	}
	hostname = strings.ToLower(hostname)
	if port == "" || port == defaultPortForScheme(scheme) {
		return hostname, true
	}
	return strings.ToLower(net.JoinHostPort(hostname, port)), true
}

func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func bearerToken(value string) string {
	fields := strings.Fields(value)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return ""
	}
	return fields[1]
}
