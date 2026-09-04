module github.com/rstreamlabs/rstream-examples/webrtc-video/producer

go 1.27.0

require (
	github.com/go-gst/go-gst v1.4.0
	github.com/pion/ice/v4 v4.4.1
	github.com/pion/interceptor v0.1.47
	github.com/pion/logging v0.2.4
	github.com/pion/rtcp v1.2.17
	github.com/pion/rtp v1.10.5
	github.com/pion/sdp/v3 v3.0.19
	github.com/pion/webrtc/v4 v4.2.19
	github.com/prometheus/client_golang v1.24.1
	github.com/prometheus/client_model v0.6.3
	github.com/rstreamlabs/rstream-go v1.30.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/eclipse-keypont/crypto11 v1.6.8 // indirect
	github.com/go-gst/go-glib v1.4.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/johnstarich/go/dns v0.2.5 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/miekg/pkcs11 v1.1.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pion/datachannel v1.6.2 // indirect
	github.com/pion/dtls/v3 v3.1.8 // indirect
	github.com/pion/mdns/v2 v2.1.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.11.1 // indirect
	github.com/pion/srtp/v3 v3.0.13 // indirect
	github.com/pion/stun/v3 v3.1.7 // indirect
	github.com/pion/transport/v4 v4.1.0 // indirect
	github.com/pion/turn/v5 v5.0.13 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.60.0 // indirect
	github.com/thales-e-security/pool v0.0.2 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/pion/interceptor => github.com/rstreamlabs/pion-interceptor v0.1.48-0.20260822042700-f4a26c59b0fa
