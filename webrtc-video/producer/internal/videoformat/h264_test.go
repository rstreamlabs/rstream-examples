package videoformat

import (
	"strings"
	"testing"
)

func testFormat(w, h, n, d int) Format {
	return Format{Width: w, Height: h, FrameRate: FrameRate{Numerator: n, Denominator: d}}
}

func TestH264LevelBoundsIncludeProcessingRateAndMacroblockRounding(t *testing.T) {
	limits, err := H264Bounds("profile-level-id=42e01f;packetization-mode=1", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		format  Format
		bitrate int
		allowed bool
	}{
		{"720p30 at limit", testFormat(1280, 720, 30, 1), 14000, true},
		{"fractional rate", testFormat(1280, 720, 30000, 1001), 14000, true},
		{"processing rate exceeded", testFormat(1280, 720, 30001, 1000), 1000, false},
		{"macroblock rounding", testFormat(1282, 720, 1, 1), 1000, false},
		{"1080p at low rate still too large", testFormat(1920, 1080, 1, 1), 1000, false},
		{"extreme aspect ratio", testFormat(3600, 16, 1, 1), 1000, false},
		{"bitrate exceeded", testFormat(320, 180, 15, 1), 14001, false},
		{"invalid rate", testFormat(320, 180, 0, 1), 1000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := limits.Allows(test.format, test.bitrate); got != test.allowed {
				t.Fatalf("Allows(%s, %d) = %v", test.format, test.bitrate, got)
			}
		})
	}
}

func TestH264ReceiveExtensionsNeverIncreaseSenderLevel(t *testing.T) {
	for _, extension := range []string{"max-recv-level=e028", "max-fs=8192;max-mbps=245760;max-br=20000"} {
		fmtp := "profile-level-id=42e01f;packetization-mode=1;" + extension
		for _, receiver := range []bool{false, true} {
			limits, err := H264Bounds(fmtp, receiver)
			if err != nil {
				t.Fatal(err)
			}
			if limits.Allows(testFormat(1920, 1080, 30, 1), 16000) != receiver {
				t.Fatalf("receiver=%v: %+v", receiver, limits)
			}
		}
	}
	// max-br is already kbit/s; multiplying it by the High-profile factor
	// would silently authorize a bitrate the receiver did not advertise.
	limits, err := H264Bounds("profile-level-id=64001f;packetization-mode=1;max-br=18000", true)
	if err != nil || limits.BitrateKbps != 18000 {
		t.Fatalf("High max-br: %+v, %v", limits, err)
	}
	limits, err = H264Bounds("profile-level-id=64001f;packetization-mode=1", true)
	if err != nil || limits.BitrateKbps != 17500 {
		t.Fatalf("High level bound: %+v, %v", limits, err)
	}
}

func TestH264Level1bOrderingAndRepresentations(t *testing.T) {
	a, err := H264Bounds("profile-level-id=42f00b;packetization-mode=1", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := H264Bounds("profile-level-id=420009;packetization-mode=1", false)
	if err != nil || a != b || a.BitrateKbps != 128 {
		t.Fatalf("level 1b: %+v %+v %v", a, b, err)
	}
	for _, fmtp := range []string{
		"profile-level-id=42000a;packetization-mode=1;max-recv-level=f00b",
		"profile-level-id=42f00b;packetization-mode=1;max-recv-level=e00b",
	} {
		if _, err := H264Bounds(fmtp, true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestH264BoundsRejectAmbiguousAndInvalidReceiverParameters(t *testing.T) {
	base := "profile-level-id=42e01f;packetization-mode=1"
	for _, fmtp := range []string{
		"", "packetization-mode=1", "profile-level-id=42e01f", "profile-level-id=42e01f;packetization-mode=0",
		"profile-level-id=zz001f;packetization-mode=1", "profile-level-id=01001f;packetization-mode=1",
		"profile-level-id=42e0ff;packetization-mode=1", base + ";PROFILE-LEVEL-ID=42e028",
		base + ";max-recv-level=28", base + ";max-recv-level=e01f", base + ";max-recv-level=e01e",
		"profile-level-id=42000b;packetization-mode=1;max-recv-level=f00b",
		base + ";max-fs=3599", base + ";max-mbps=-1", base + ";max-br=13999",
		base + ";max-mbps=2147483648", base + ";max-fs=NaN", base + ";max-br=1.5",
		"profile-level-id=64001f;packetization-mode=1;max-br=16000",
		base + ";flag", base + ";max-fs=", strings.Repeat(" ", 4097),
	} {
		if _, err := H264Bounds(fmtp, true); err == nil {
			t.Errorf("accepted invalid parameters %q", fmtp)
		}
	}
}
