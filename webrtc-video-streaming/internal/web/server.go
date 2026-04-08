package web

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
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

//go:embed embed/index.html generated/*
var assets embed.FS

type Info struct {
	LocalURL        string                 `json:"localURL"`
	PublicURL       *string                `json:"publicURL,omitempty"`
	ViewerURL       *string                `json:"viewerURL,omitempty"`
	AuthMode        config.TunnelAuthMode  `json:"authMode"`
	VideoMimeType   string                 `json:"videoMimeType"`
	TWCCEnabled     bool                   `json:"twccEnabled"`
	NACKEnabled     bool                   `json:"nackEnabled"`
	RTXEnabled      bool                   `json:"rtxEnabled"`
	FlexFECEnabled  bool                   `json:"flexFECEnabled"`
	AdaptiveBackend config.AdaptiveBackend `json:"adaptiveBackend"`
}

func (i Info) HasDedicatedViewerURL() bool {
	return i.ViewerURL != nil && i.PublicURL != nil && *i.ViewerURL != *i.PublicURL
}

type Server struct {
	logger      *logs.Logger
	logHub      *logs.Hub
	createTURN  func(context.Context) (*rstream.TURNCredentials, error)
	openSession func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error)
	mu          sync.RWMutex
	info        Info
	upgrader    websocket.Upgrader
}

func NewServer(
	logger *logs.Logger,
	logHub *logs.Hub,
	createTURN func(context.Context) (*rstream.TURNCredentials, error),
	openSession func(context.Context, func(rtc.SignalMessage) error) (*rtc.Session, error),
) *Server {
	return &Server{
		logger:      logger,
		logHub:      logHub,
		createTURN:  createTURN,
		openSession: openSession,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
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
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/app.js", s.handleStatic)
	mux.HandleFunc("/app.css", s.handleStatic)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/turn", s.handleTURN)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
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

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	info := s.info
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleTURN(w http.ResponseWriter, r *http.Request) {
	credentials, err := s.createTURN(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, credentials)
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
		_ = writer.WriteJSON(rtc.SignalMessage{
			Type:    "log",
			Message: entry.Message,
		})
	}
	logEvents, unsubscribe := s.logHub.Subscribe()
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
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
					return
				}
			case <-time.After(30 * time.Second):
				if err := writer.WriteControl(websocket.PingMessage, nil); err != nil {
					return
				}
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
				_ = writer.WriteJSON(rtc.SignalMessage{
					Type:    "error",
					Message: err.Error(),
				})
			}
		case "candidate", "webrtc.candidate":
			if err := session.AddICECandidate(message.Candidate, message.SDPMid, message.SDPMLineIndex); err != nil {
				_ = writer.WriteJSON(rtc.SignalMessage{
					Type:    "error",
					Message: err.Error(),
				})
			}
		case "ping":
			_ = writer.WriteJSON(rtc.SignalMessage{Type: "pong"})
		}
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, name, contentType string) {
	body, err := assets.ReadFile(name)
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
