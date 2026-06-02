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

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/logs"
	rtc "github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/webrtc"
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

type Server struct {
	logger      *logs.Logger
	logHub      *logs.Hub
	createTURN  func(context.Context) (*rstream.TURNCredentials, error)
	openSession func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error)
	viewer      bool
	mu          sync.RWMutex
	info        Info
	upgrader    websocket.Upgrader
}

type ServerOptions struct {
	Viewer bool
}

func NewServer(
	logger *logs.Logger,
	logHub *logs.Hub,
	createTURN func(context.Context) (*rstream.TURNCredentials, error),
	openSession func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error),
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
	return &Server{
		logger:      logger,
		logHub:      logHub,
		createTURN:  createTURN,
		openSession: openSession,
		viewer:      viewer,
		upgrader: websocket.Upgrader{
			CheckOrigin: checkOrigin,
		},
	}
}

func (s *Server) SetInfo(info Info) {
	s.mu.Lock()
	s.info = info
	s.mu.Unlock()
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
	}
}

func (s *Server) mountAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /api/turn", s.handleAPITURN)
}

func (s *Server) mountRealtimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ws", s.handleWS)
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

func (s *Server) handleAPIStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	info := s.info
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAPITURN(w http.ResponseWriter, r *http.Request) {
	credentials, err := s.createTURN(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, credentials)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	writer := &wsWriter{conn: conn}
	send := func(message rtc.SignalMessage) error {
		return writer.WriteJSON(message)
	}
	session, err := s.openSession(r.Context(), send)
	if err != nil {
		s.logger.Warn("WebRTC session creation failed: %v", err)
		_ = writer.WriteJSON(rtc.SignalMessage{
			Type:    "error",
			Message: err.Error(),
		})
		_ = conn.Close()
		return
	}
	if err := writer.WriteJSON(rtc.SignalMessage{
		Type:     "session.ready",
		ViewerID: session.ID(),
	}); err != nil {
		session.Close("failed to write the session bootstrap message")
		_ = conn.Close()
		return
	}
	recent := s.logHub.Recent()
	for _, entry := range recent {
		if err := writer.WriteJSON(rtc.SignalMessage{
			Type:    "log",
			Message: entry.Message,
		}); err != nil {
			session.Close("websocket write failed")
			_ = conn.Close()
			return
		}
	}
	logEvents, unsubscribe := s.logHub.Subscribe()
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case entry, ok := <-logEvents:
				if !ok {
					return
				}
				if err := writer.WriteJSON(rtc.SignalMessage{
					Type:    "log",
					Message: entry.Message,
				}); err != nil {
					session.Close("websocket write failed")
					_ = conn.Close()
					return
				}
			case <-ticker.C:
				if err := writer.WriteControl(websocket.PingMessage, nil); err != nil {
					session.Close("websocket write failed")
					_ = conn.Close()
					return
				}
			case <-session.Done():
				return
			}
		}
	}()
	go func() {
		<-session.Done()
		_ = conn.Close()
	}()
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		var message rtc.SignalMessage
		if err := conn.ReadJSON(&message); err != nil {
			session.Close("websocket closed")
			_ = conn.Close()
			<-done
			return
		}
		switch strings.TrimSpace(message.Type) {
		case "offer", "webrtc.offer":
			if err := session.HandleOffer(message.SDP); err != nil {
				s.logger.Warn("Viewer %s offer handling failed: %v", session.ID(), err)
				if writeErr := writer.WriteJSON(rtc.SignalMessage{
					Type:    "error",
					Message: err.Error(),
				}); writeErr != nil {
					session.Close("websocket write failed")
					_ = conn.Close()
					<-done
					return
				}
			}
		case "candidate", "webrtc.candidate":
			if err := session.AddICECandidate(
				message.Candidate,
				message.SDPMid,
				message.SDPMLineIndex,
				message.UsernameFragment,
			); err != nil {
				s.logger.Warn("Viewer %s ICE candidate handling failed: %v", session.ID(), err)
				if writeErr := writer.WriteJSON(rtc.SignalMessage{
					Type:    "error",
					Message: err.Error(),
				}); writeErr != nil {
					session.Close("websocket write failed")
					_ = conn.Close()
					<-done
					return
				}
			}
		case "ping":
			if err := writer.WriteJSON(rtc.SignalMessage{Type: "pong"}); err != nil {
				session.Close("websocket write failed")
				_ = conn.Close()
				<-done
				return
			}
		}
	}
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

type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) WriteJSON(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(value)
}

func (w *wsWriter) WriteControl(messageType int, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteControl(messageType, data, time.Now().Add(5*time.Second))
}
