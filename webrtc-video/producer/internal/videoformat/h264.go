package videoformat

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// H264Limits covers progressive frame size, processing rate and a conservative
// bitrate ceiling. It does not replace SPS/DPB/HRD conformance checks or codec
// profile matching by the WebRTC stack.
type H264Limits struct {
	Macroblocks          int64
	MacroblocksPerSecond int64
	BitrateKbps          int64
}

// H.264 Annex A, table A-1 (MaxFS, MaxMBPS, MaxBR). Reference data is also
// published at https://ffmpeg.org/doxygen/7.1/h264__levels_8c_source.html .
var h264Levels = map[byte]H264Limits{
	10: {99, 1485, 64}, 9: {99, 1485, 128}, 11: {396, 3000, 192},
	12: {396, 6000, 384}, 13: {396, 11880, 768}, 20: {396, 11880, 2000},
	21: {792, 19800, 4000}, 22: {1620, 20250, 4000}, 30: {1620, 40500, 10000},
	31: {3600, 108000, 14000}, 32: {5120, 216000, 20000},
	40: {8192, 245760, 20000}, 41: {8192, 245760, 50000}, 42: {8704, 522240, 50000},
	50: {22080, 589824, 135000}, 51: {36864, 983040, 240000}, 52: {36864, 2073600, 240000},
	60: {139264, 4177920, 240000}, 61: {139264, 8355840, 480000}, 62: {139264, 16711680, 800000},
}

// H264Bounds reads RFC 6184 parameters. Receive-only extensions never raise the
// sender's configured level bounds. An explicit profile/level is required by
// the optional format-control feature instead of guessing missing capabilities.
func H264Bounds(fmtp string, receiver bool) (H264Limits, error) {
	if len(fmtp) > 4096 {
		return H264Limits{}, errors.New("H264 parameters exceed the supported size")
	}
	parameters := make(map[string]string)
	for _, part := range strings.Split(fmtp, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if !ok || key == "" || value == "" || len(parameters) >= 32 {
			return H264Limits{}, errors.New("invalid H264 parameters")
		}
		if _, duplicate := parameters[key]; duplicate {
			return H264Limits{}, errors.New("duplicate H264 parameter")
		}
		parameters[key] = value
	}
	profile, err := hex.DecodeString(parameters["profile-level-id"])
	if err != nil || len(profile) != 3 || parameters["packetization-mode"] != "1" {
		return H264Limits{}, errors.New("source format control requires explicit H264 profile-level-id and packetization-mode=1")
	}
	limits, err := h264Level(profile[1], profile[2])
	if err != nil {
		return limits, err
	}
	factor := int64(1000)
	switch profile[0] {
	case 66, 77, 88:
	case 100:
		factor = 1250
	case 110:
		factor = 3000
	case 122, 244:
		factor = 4000
	default:
		return H264Limits{}, errors.New("unsupported H264 profile for source format control")
	}
	if receiver {
		if maximum, present := parameters["max-recv-level"]; present {
			bytes, err := hex.DecodeString(maximum)
			if err != nil || len(bytes) != 2 {
				return H264Limits{}, errors.New("invalid H264 max-recv-level")
			}
			higher, err := h264Level(bytes[0], bytes[1])
			if err != nil || higher.Macroblocks < limits.Macroblocks || higher.MacroblocksPerSecond < limits.MacroblocksPerSecond || higher.BitrateKbps < limits.BitrateKbps || higher == limits {
				return H264Limits{}, errors.New("H264 max-recv-level must describe a higher supported level")
			}
			limits = higher
		}
	}
	// max-br uses fixed 1000-bit VCL units, not the profile-specific factor
	// used by Annex A's table. Convert the level before reading extensions.
	limits.BitrateKbps = limits.BitrateKbps * factor / 1000
	if receiver {
		for key, target := range map[string]*int64{"max-fs": &limits.Macroblocks, "max-mbps": &limits.MacroblocksPerSecond, "max-br": &limits.BitrateKbps} {
			if raw, present := parameters[key]; present {
				value, err := strconv.ParseUint(raw, 10, 31)
				if err != nil || int64(value) < *target {
					return H264Limits{}, fmt.Errorf("invalid H264 %s", key)
				}
				*target = int64(value)
			}
		}
	}
	// The total encoder target is bounded by the VCL rate; this is conservative
	// relative to the higher NAL rate allowance, even with in-band headers.
	return limits, nil
}

func h264Level(constraints, level byte) (H264Limits, error) {
	if level == 11 && constraints&0x10 != 0 {
		level = 9 // Level 1b is also represented by constraint_set3 + level 1.1.
	}
	limits, ok := h264Levels[level]
	if !ok {
		return H264Limits{}, errors.New("unsupported H264 level")
	}
	return limits, nil
}

func (l H264Limits) Allows(format Format, bitrateKbps int) bool {
	format, err := format.Normalize()
	if err != nil || bitrateKbps <= 0 || int64(bitrateKbps) > l.BitrateKbps {
		return false
	}
	w, h := int64((format.Width+15)/16), int64((format.Height+15)/16)
	return w*h <= l.Macroblocks && w*w <= 8*l.Macroblocks && h*h <= 8*l.Macroblocks &&
		w*h*int64(format.FrameRate.Numerator) <= l.MacroblocksPerSecond*int64(format.FrameRate.Denominator)
}
