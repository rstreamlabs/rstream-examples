module github.com/rstreamlabs/rstream-examples/webrtc-video/distributor

go 1.26.6

require (
	github.com/pion/interceptor v0.1.47
	github.com/pion/logging v0.2.4
	github.com/pion/rtcp v1.2.17
	github.com/pion/rtp v1.10.5
	github.com/pion/sdp/v3 v3.0.19
	github.com/pion/webrtc/v4 v4.2.17
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/pion/datachannel v1.6.2 // indirect
	github.com/pion/dtls/v3 v3.1.5 // indirect
	github.com/pion/ice/v4 v4.4.0 // indirect
	github.com/pion/mdns/v2 v2.1.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.11.1 // indirect
	github.com/pion/srtp/v3 v3.0.13 // indirect
	github.com/pion/stun/v3 v3.1.7 // indirect
	github.com/pion/transport/v4 v4.1.0 // indirect
	github.com/pion/turn/v5 v5.0.13 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)

replace github.com/pion/interceptor => github.com/rstreamlabs/pion-interceptor v0.1.48-0.20260817140720-195b94231732

replace github.com/pion/webrtc/v4 => github.com/rstreamlabs/pion-webrtc/v4 v4.2.19-0.20260817140720-926abfa31a52

replace github.com/pion/ice/v4 => github.com/rstreamlabs/ice/v4 v4.4.2-0.20260820223743-efa797c6555b
