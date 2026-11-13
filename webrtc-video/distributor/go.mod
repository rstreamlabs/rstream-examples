module github.com/rstreamlabs/rstream-examples/webrtc-video/distributor

go 1.27.4

require (
	github.com/pion/ice/v4 v4.4.2
	github.com/pion/interceptor v0.1.48
	github.com/pion/logging v0.2.4
	github.com/pion/rtcp v1.2.17
	github.com/pion/rtp v1.10.5
	github.com/pion/sdp/v3 v3.0.19
	github.com/pion/transport/v4 v4.1.3
	github.com/pion/turn/v5 v5.1.0
	github.com/pion/webrtc/v4 v4.2.20
	golang.org/x/sys v0.50.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/pion/datachannel v1.6.2 // indirect
	github.com/pion/dtls/v3 v3.1.15 // indirect
	github.com/pion/mdns/v2 v2.2.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.11.1 // indirect
	github.com/pion/srtp/v3 v3.0.13 // indirect
	github.com/pion/stun/v3 v3.1.7 // indirect
	github.com/pion/stun/v4 v4.0.0 // indirect
	github.com/pion/transport/v5 v5.1.1 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.59.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace github.com/pion/interceptor => github.com/rstreamlabs/pion-interceptor v0.1.48-0.20261004062019-94d47092806b

replace github.com/pion/webrtc/v4 => github.com/rstreamlabs/pion-webrtc/v4 v4.2.19-0.20261112225417-baefa1001540
