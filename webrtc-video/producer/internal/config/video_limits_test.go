package config

import "testing"

func TestH264EncoderCeilingFitsDeclaredLevel(t *testing.T) {
	for _, adaptive := range []bool{false, true} {
		cfg := Default()
		cfg.Media.Mode = MediaModePerViewer
		cfg.WebRTC.Adaptive.Enabled = adaptive
		if adaptive {
			cfg.WebRTC.Adaptive.TWCCGCC.MaxBitrateKbps = 15000
		} else {
			cfg.WebRTC.InitialBitrateKbps = 15000
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted 15 Mbit/s above level 3.1 with adaptive=%v", adaptive)
		}
		higher := "packetization-mode=1;profile-level-id=42e028;level-asymmetry-allowed=1"
		cfg.WebRTC.Video.SDPFmtpLine = &higher
		if err := cfg.Validate(); err != nil {
			t.Fatalf("level 4 allows 15 Mbit/s: %v", err)
		}
	}
}

func TestH264RequiresUnambiguousSenderParameters(t *testing.T) {
	for _, parameters := range []string{"", "packetization-mode=1", "profile-level-id=42e01f;packetization-mode=0", "profile-level-id=42e01f;profile-level-id=42e028;packetization-mode=1"} {
		cfg := Default()
		cfg.WebRTC.Video.SDPFmtpLine = &parameters
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted ambiguous sender parameters %q", parameters)
		}
	}
}
