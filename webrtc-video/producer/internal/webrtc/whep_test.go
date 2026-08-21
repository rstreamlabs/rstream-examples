package webrtc

import (
	"strings"
	"testing"
)

func addRTCPMuxOnly(raw string) string {
	return strings.ReplaceAll(raw, "a=rtcp-mux\r\n", "a=rtcp-mux\r\na=rtcp-mux-only\r\na=msid:rstream-whep rstream-video\r\n")
}

func TestParseWHEPICEFragmentPreservesCredentialsAndCandidates(t *testing.T) {
	raw := "a=group:BUNDLE video\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 106\r\n" +
		"a=mid:video\r\n" +
		"a=ice-ufrag:client-1\r\n" +
		"a=ice-pwd:client-password-1\r\n" +
		"a=candidate:1 1 udp 2130706431 192.0.2.1 5000 typ host\r\n" +
		"a=end-of-candidates\r\n"
	fragment, err := parseWHEPICEFragment(raw)
	if err != nil {
		t.Fatalf("parse ICE fragment: %v", err)
	}
	if fragment.ufrag != "client-1" || fragment.pwd != "client-password-1" {
		t.Fatalf("credentials = %q/%q", fragment.ufrag, fragment.pwd)
	}
	if len(fragment.candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(fragment.candidates))
	}
	candidate := fragment.candidates[0]
	if candidate.Candidate != "candidate:1 1 udp 2130706431 192.0.2.1 5000 typ host" {
		t.Fatalf("candidate = %q", candidate.Candidate)
	}
	if candidate.SDPMid == nil || *candidate.SDPMid != "video" {
		t.Fatalf("candidate mid = %v", candidate.SDPMid)
	}
	if candidate.UsernameFragment == nil || *candidate.UsernameFragment != "client-1" {
		t.Fatalf("candidate ufrag = %v", candidate.UsernameFragment)
	}
}

func TestParseWHEPICEFragmentRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing password", raw: "a=ice-ufrag:client\r\n"},
		{name: "candidate without mid", raw: "m=video 9 UDP/TLS/RTP/SAVPF 106\r\na=candidate:1 1 udp 1 192.0.2.1 5000 typ host\r\n"},
		{name: "conflicting credentials", raw: "a=ice-ufrag:one\r\na=ice-pwd:password-one\r\nm=video 9 UDP/TLS/RTP/SAVPF 106\r\na=mid:0\r\na=ice-ufrag:two\r\na=ice-pwd:password-two\r\n"},
		{name: "no ICE data", raw: "a=group:BUNDLE 0\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseWHEPICEFragment(test.raw); err == nil {
				t.Fatal("expected invalid ICE fragment to fail")
			}
		})
	}
	var oversized strings.Builder
	oversized.WriteString("a=ice-ufrag:client\r\na=ice-pwd:password\r\nm=video 9 UDP/TLS/RTP/SAVPF 106\r\na=mid:0\r\n")
	for i := 0; i <= maxWHEPRemoteCandidates; i++ {
		oversized.WriteString("a=candidate:1 1 udp 1 192.0.2.1 5000 typ host\r\n")
	}
	if _, err := parseWHEPICEFragment(oversized.String()); err == nil {
		t.Fatal("expected excess candidates to fail")
	}
}

func TestValidateWHEPCandidatesRejectsMalformedCandidateBeforeRestart(t *testing.T) {
	fragment, err := parseWHEPICEFragment("a=ice-ufrag:client\r\na=ice-pwd:client-password\r\nm=video 9 UDP/TLS/RTP/SAVPF 106\r\na=mid:0\r\na=candidate:not-an-ice-candidate\r\n")
	if err != nil {
		t.Fatalf("parse syntactically valid SDP fragment: %v", err)
	}
	if err := validateWHEPCandidates(fragment.candidates); err == nil {
		t.Fatal("expected malformed ICE candidate to fail validation")
	}
}

func TestValidateWHEPCandidatesAcceptsNumericFoundation(t *testing.T) {
	fragment, err := parseWHEPICEFragment("a=ice-ufrag:client\r\na=ice-pwd:client-password\r\nm=video 9 UDP/TLS/RTP/SAVPF 106\r\na=mid:0\r\na=candidate:2878742611 1 udp 2130706431 127.0.0.1 52633 typ host ufrag client\r\n")
	if err != nil {
		t.Fatalf("parse MediaMTX ICE fragment: %v", err)
	}
	if err := validateWHEPCandidates(fragment.candidates); err != nil {
		t.Fatalf("validate MediaMTX ICE candidate: %v", err)
	}
}

func TestReplaceICECredentialsRemovesPreviousCandidateGeneration(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 0 0 IN IP4 0.0.0.0\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 106\r\n" +
		"a=mid:0\r\n" +
		"a=ice-ufrag:old\r\n" +
		"a=ice-pwd:old-password\r\n" +
		"a=candidate:1 1 udp 1 192.0.2.1 5000 typ host\r\n" +
		"a=end-of-candidates\r\n"
	updated, err := replaceICECredentials(raw, "new", "new-password")
	if err != nil {
		t.Fatalf("replace ICE credentials: %v", err)
	}
	if !strings.Contains(updated, "a=ice-ufrag:new\r\n") || !strings.Contains(updated, "a=ice-pwd:new-password\r\n") {
		t.Fatalf("updated SDP has no new credentials:\n%s", updated)
	}
	if strings.Contains(updated, "a=ice-ufrag:old") || strings.Contains(updated, "a=candidate:") || strings.Contains(updated, "a=end-of-candidates") {
		t.Fatalf("updated SDP retains the previous ICE generation:\n%s", updated)
	}
}

func TestValidateWHEPOfferEnforcesStreamingConstraints(t *testing.T) {
	valid := "v=0\r\n" +
		"o=- 0 0 IN IP4 0.0.0.0\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"a=group:BUNDLE video\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 106\r\n" +
		"a=mid:video\r\n" +
		"a=recvonly\r\n" +
		"a=rtcp-mux\r\n" +
		"a=rtcp-mux-only\r\n" +
		"a=msid:rstream-whep rstream-video\r\n"
	if err := validateWHEPOffer(valid); err != nil {
		t.Fatalf("valid WHEP offer: %v", err)
	}
	nativeMediaMTX := strings.ReplaceAll(valid, "a=rtcp-mux-only\r\n", "")
	nativeMediaMTX = strings.ReplaceAll(nativeMediaMTX, "a=msid:rstream-whep rstream-video\r\n", "")
	if err := validateWHEPOffer(nativeMediaMTX); err == nil {
		t.Fatal("strict WHEP validation accepted the MediaMTX native offer")
	}
	if err := validateWHEPOfferProfile(nativeMediaMTX, true); err != nil {
		t.Fatalf("MediaMTX native WHEP offer: %v", err)
	}
	withoutRTCPMux := strings.ReplaceAll(nativeMediaMTX, "a=rtcp-mux\r\n", "")
	if err := validateWHEPOfferProfile(withoutRTCPMux, true); err == nil {
		t.Fatal("MediaMTX compatibility accepted an offer without RTCP multiplexing")
	}
	tests := []struct {
		name string
		raw  string
	}{
		{name: "no bundle", raw: strings.Replace(valid, "a=group:BUNDLE video\r\n", "", 1)},
		{name: "non bundled mid", raw: strings.Replace(valid, "a=group:BUNDLE video", "a=group:BUNDLE other", 1)},
		{name: "no mux only", raw: strings.Replace(valid, "a=rtcp-mux-only\r\n", "", 1)},
		{name: "no media stream", raw: strings.Replace(valid, "a=msid:rstream-whep rstream-video\r\n", "", 1)},
		{name: "wrong direction", raw: strings.Replace(valid, "a=recvonly", "a=sendonly", 1)},
		{name: "no video", raw: strings.Replace(valid, "m=video", "m=application", 1)},
		{name: "two videos", raw: valid + "m=video 9 UDP/TLS/RTP/SAVPF 106\r\na=mid:video2\r\na=recvonly\r\na=rtcp-mux-only\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateWHEPOffer(test.raw); err == nil {
				t.Fatal("expected WHEP offer to fail")
			}
		})
	}
}
