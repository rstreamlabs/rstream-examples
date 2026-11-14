module github.com/rstreamlabs/rstream-examples/webrtc-video/producer

go 1.27.4

require (
	github.com/go-gst/go-glib v1.4.0
	github.com/go-gst/go-gst v1.4.0
	github.com/pion/dtls/v3 v3.1.15
	github.com/pion/ice/v4 v4.4.9
	github.com/pion/interceptor v0.1.51
	github.com/pion/logging v0.2.4
	github.com/pion/rtcp v1.2.20
	github.com/pion/rtp v1.10.6
	github.com/pion/sdp/v3 v3.0.20
	github.com/pion/transport/v4 v4.1.3
	github.com/pion/webrtc/v4 v4.2.26
	github.com/prometheus/client_golang v1.25.0
	github.com/prometheus/client_model v0.6.3
	github.com/quic-go/connect-ip-go v0.6.0
	github.com/quic-go/masque-go v0.6.0
	github.com/quic-go/quic-go v0.64.1
	github.com/quic-go/webtransport-go v0.13.0
	github.com/rstreamlabs/rstream-go v1.37.1-0.20261114141308-06bc287010aa
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dunglas/httpsfv v1.1.2 // indirect
	github.com/eclipse-keypont/crypto11 v1.6.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/johnstarich/go/dns v0.2.5 // indirect
	github.com/klauspost/compress v1.20.4 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/miekg/pkcs11 v1.1.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pandatix/go-abnf v0.5.5 // indirect
	github.com/pion/datachannel v1.6.4 // indirect
	github.com/pion/mdns/v2 v2.2.2 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.12.1 // indirect
	github.com/pion/srtp/v3 v3.1.3 // indirect
	github.com/pion/stun/v3 v3.1.7 // indirect
	github.com/pion/stun/v4 v4.0.1 // indirect
	github.com/pion/transport/v5 v5.1.5 // indirect
	github.com/pion/turn/v5 v5.1.3 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/common v0.74.0 // indirect
	github.com/prometheus/procfs v0.23.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/thales-e-security/pool v0.0.2 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.59.0 // indirect
	golang.org/x/exp v0.0.0-20261113113407-e3970619a1a4 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

replace github.com/pion/interceptor => github.com/rstreamlabs/pion-interceptor v0.1.48-0.20261114142831-7968d9cb7554

replace github.com/pion/webrtc/v4 => github.com/rstreamlabs/pion-webrtc/v4 v4.2.19-0.20261114134539-8c811d4c3b89
