package webrtc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

const (
	maxWHEPRemoteCandidates  = 64
	whepRestartGatherTimeout = 10 * time.Second
)

type whepICEFragment struct {
	ufrag      string
	pwd        string
	candidates []webrtc.ICECandidateInit
}

type whepOfferProfile struct {
	mediaMTXNative bool
}

func validateWHEPOffer(raw string) error {
	return validateWHEPOfferProfile(raw, false)
}

func validateWHEPOfferProfile(raw string, allowMediaMTXNativeOffer bool) error {
	_, err := inspectWHEPOfferProfile(raw, allowMediaMTXNativeOffer)
	return err
}

func inspectWHEPOfferProfile(raw string, allowMediaMTXNativeOffer bool) (whepOfferProfile, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return whepOfferProfile{}, fmt.Errorf("parse WHEP offer: %w", err)
	}
	bundle := bundleMIDs(description.Attributes)
	if len(bundle) == 0 {
		return whepOfferProfile{}, errors.New("WHEP offer does not use BUNDLE")
	}
	profile := whepOfferProfile{}
	video := 0
	audio := 0
	streamID := ""
	for _, media := range description.MediaDescriptions {
		if media == nil || media.MediaName.Port.Value == 0 {
			continue
		}
		mid, ok := attribute(media.Attributes, "mid")
		if !ok || !bundle[strings.TrimSpace(mid)] {
			return whepOfferProfile{}, errors.New("WHEP offer does not use max-bundle")
		}
		switch media.MediaName.Media {
		case "video", "audio":
			if _, ok := attribute(media.Attributes, "rtcp-mux-only"); !ok {
				if !allowMediaMTXNativeOffer || !hasAttribute(media.Attributes, "rtcp-mux") {
					return whepOfferProfile{}, fmt.Errorf("WHEP %s section does not require RTCP multiplexing", media.MediaName.Media)
				}
				profile.mediaMTXNative = true
			}
			if direction(media.Attributes) == "sendonly" || direction(media.Attributes) == "inactive" {
				return whepOfferProfile{}, fmt.Errorf("WHEP %s section has invalid %s direction", media.MediaName.Media, direction(media.Attributes))
			}
			msid, ok := attribute(media.Attributes, "msid")
			if !ok && allowMediaMTXNativeOffer {
				profile.mediaMTXNative = true
				if media.MediaName.Media == "video" {
					video++
				} else {
					audio++
				}
				continue
			}
			fields := strings.Fields(msid)
			if !ok || len(fields) < 2 {
				return whepOfferProfile{}, fmt.Errorf("WHEP %s section has no MediaStream identifier", media.MediaName.Media)
			}
			if streamID == "" {
				streamID = fields[0]
			} else if streamID != fields[0] {
				return whepOfferProfile{}, errors.New("WHEP offer contains multiple MediaStreams")
			}
			if media.MediaName.Media == "video" {
				video++
			} else {
				audio++
			}
		}
	}
	if video != 1 {
		return whepOfferProfile{}, fmt.Errorf("WHEP offer has %d active video sections, want 1", video)
	}
	if audio > 1 {
		return whepOfferProfile{}, fmt.Errorf("WHEP offer has %d active audio sections, want at most 1", audio)
	}
	return profile, nil
}

func prepareWHEPAnswer(raw string) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", fmt.Errorf("parse WHEP answer: %w", err)
	}
	bundle := bundleMIDs(description.Attributes)
	if len(bundle) == 0 {
		return "", errors.New("WHEP answer does not use BUNDLE")
	}
	video := 0
	for _, media := range description.MediaDescriptions {
		if media == nil || media.MediaName.Port.Value == 0 {
			continue
		}
		mid, ok := attribute(media.Attributes, "mid")
		if !ok || !bundle[strings.TrimSpace(mid)] {
			return "", errors.New("WHEP answer does not use max-bundle")
		}
		if media.MediaName.Media != "video" && media.MediaName.Media != "audio" {
			continue
		}
		if direction(media.Attributes) != "sendonly" {
			return "", fmt.Errorf("WHEP %s answer is not sendonly", media.MediaName.Media)
		}
		if _, ok := attribute(media.Attributes, "rtcp-mux-only"); !ok {
			media.Attributes = append(media.Attributes, sdp.Attribute{Key: "rtcp-mux-only"})
		}
		if media.MediaName.Media == "video" {
			video++
		}
	}
	if video != 1 {
		return "", fmt.Errorf("WHEP answer has %d active video sections, want 1", video)
	}
	encoded, err := description.Marshal()
	if err != nil {
		return "", fmt.Errorf("encode WHEP answer: %w", err)
	}
	return string(encoded), nil
}

func bundleMIDs(values []sdp.Attribute) map[string]bool {
	for _, value := range values {
		fields := strings.Fields(value.Value)
		if value.Key != "group" || len(fields) < 2 || !strings.EqualFold(fields[0], "BUNDLE") {
			continue
		}
		out := make(map[string]bool, len(fields)-1)
		for _, mid := range fields[1:] {
			out[mid] = true
		}
		return out
	}
	return nil
}

func direction(values []sdp.Attribute) string {
	for _, candidate := range []string{"sendrecv", "recvonly", "sendonly", "inactive"} {
		if _, ok := attribute(values, candidate); ok {
			return candidate
		}
	}
	return "sendrecv"
}

func (s *Session) HandleWHEPICE(ctx context.Context, raw string, restart bool) (response string, err error) {
	fragment, err := parseWHEPICEFragment(raw)
	if err != nil {
		return "", err
	}
	restartApplied := false
	defer func() {
		if err != nil && restartApplied {
			s.Close("WHEP ICE restart failed after applying remote credentials")
		}
	}()
	s.signalingMu.Lock()
	defer s.signalingMu.Unlock()
	remote := s.pc.RemoteDescription()
	if remote == nil {
		return "", errors.New("the WHEP session has no remote description")
	}
	currentUfrag, currentPwd, err := iceCredentials(remote.SDP)
	if err != nil {
		return "", fmt.Errorf("read current ICE credentials: %w", err)
	}
	if !restart {
		if fragment.ufrag != "" && (fragment.ufrag != currentUfrag || fragment.pwd != currentPwd) {
			return "", errors.New("new ICE credentials require an ICE restart")
		}
		if s.whepRemoteCandidates+len(fragment.candidates) > maxWHEPRemoteCandidates {
			return "", errors.New("too many WHEP ICE candidates")
		}
		if err := validateWHEPCandidates(fragment.candidates); err != nil {
			return "", err
		}
		if err := s.addWHEPCandidates(fragment.candidates); err != nil {
			return "", err
		}
		s.whepRemoteCandidates += len(fragment.candidates)
		return "", nil
	}
	if fragment.ufrag == "" || fragment.pwd == "" {
		return "", errors.New("ICE restart credentials are required")
	}
	if fragment.ufrag == currentUfrag && fragment.pwd == currentPwd {
		return "", errors.New("ICE restart credentials did not change")
	}
	if len(fragment.candidates) > maxWHEPRemoteCandidates {
		return "", errors.New("too many WHEP ICE candidates")
	}
	if err := validateWHEPCandidates(fragment.candidates); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("ICE restart interrupted before negotiation: %w", err)
	}
	offer, err := replaceICECredentials(remote.SDP, fragment.ufrag, fragment.pwd)
	if err != nil {
		return "", fmt.Errorf("prepare ICE restart offer: %w", err)
	}
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", fmt.Errorf("apply ICE restart offer: %w", err)
	}
	restartApplied = true
	if err := s.addWHEPCandidates(fragment.candidates); err != nil {
		return "", err
	}
	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create ICE restart answer: %w", err)
	}
	complete := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set ICE restart answer: %w", err)
	}
	timer := time.NewTimer(whepRestartGatherTimeout)
	defer timer.Stop()
	select {
	case <-complete:
	case <-ctx.Done():
		return "", fmt.Errorf("ICE restart candidate gathering interrupted: %w", ctx.Err())
	case <-timer.C:
		return "", fmt.Errorf("ICE restart candidate gathering exceeded %s", whepRestartGatherTimeout)
	case <-s.closed:
		return "", errors.New("the WHEP session closed during ICE restart")
	}
	local := s.pc.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		return "", errors.New("the ICE restart answer is unavailable")
	}
	response, err = iceFragmentFromAnswer(local.SDP)
	if err != nil {
		return "", fmt.Errorf("encode ICE restart answer: %w", err)
	}
	s.whepRemoteCandidates = len(fragment.candidates)
	s.recordTransportNegotiation()
	return response, nil
}

func (s *Session) RefreshWHEPICE(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("refresh WHEP ICE credentials: %w", err)
	}
	select {
	case <-s.closed:
		return errors.New("the WHEP session is closed")
	default:
	}
	if s.refreshICEServers == nil {
		return nil
	}
	servers, urls, err := s.refreshICEServers(ctx)
	if err != nil {
		return err
	}
	s.signalingMu.Lock()
	defer s.signalingMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("refresh WHEP ICE credentials: %w", err)
	}
	select {
	case <-s.closed:
		return errors.New("the WHEP session closed while refreshing ICE credentials")
	default:
	}
	configuration := s.pc.GetConfiguration()
	configuration.ICEServers = servers
	if err := s.pc.SetConfiguration(configuration); err != nil {
		return fmt.Errorf("apply refreshed TURN credentials: %w", err)
	}
	s.replaceTURNURLs(urls)
	return nil
}

func validateWHEPCandidates(candidates []webrtc.ICECandidateInit) error {
	for _, candidate := range candidates {
		raw := strings.TrimPrefix(candidate.Candidate, "candidate:")
		if _, err := ice.UnmarshalCandidate(raw); err != nil {
			return fmt.Errorf("validate WHEP ICE candidate: %w", err)
		}
	}
	return nil
}

func (s *Session) addWHEPCandidates(candidates []webrtc.ICECandidateInit) error {
	for _, candidate := range candidates {
		s.recordRemoteICECandidate(candidate.Candidate)
		if err := s.pc.AddICECandidate(candidate); err != nil {
			return fmt.Errorf("apply WHEP ICE candidate: %w", err)
		}
	}
	return nil
}

func parseWHEPICEFragment(raw string) (whepICEFragment, error) {
	if strings.TrimSpace(raw) == "" {
		return whepICEFragment{}, errors.New("ICE fragment is empty")
	}
	var description sdp.SessionDescription
	prefix := "v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n"
	if err := description.Unmarshal([]byte(prefix + normalizeSDPLines(raw))); err != nil {
		return whepICEFragment{}, fmt.Errorf("parse ICE fragment: %w", err)
	}
	fragment := whepICEFragment{}
	if err := mergeICECredentials(&fragment, description.Attributes); err != nil {
		return whepICEFragment{}, err
	}
	for _, media := range description.MediaDescriptions {
		if media == nil {
			continue
		}
		if err := mergeICECredentials(&fragment, media.Attributes); err != nil {
			return whepICEFragment{}, err
		}
		mid, ok := attribute(media.Attributes, "mid")
		for _, candidate := range attributes(media.Attributes, "candidate") {
			if !ok || strings.TrimSpace(mid) == "" {
				return whepICEFragment{}, errors.New("ICE candidate media section has no mid")
			}
			if len(fragment.candidates) >= maxWHEPRemoteCandidates {
				return whepICEFragment{}, errors.New("too many WHEP ICE candidates")
			}
			midCopy := mid
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				return whepICEFragment{}, errors.New("ICE candidate is empty")
			}
			fragment.candidates = append(fragment.candidates, webrtc.ICECandidateInit{
				Candidate:        "candidate:" + candidate,
				SDPMid:           &midCopy,
				UsernameFragment: optionalString(fragment.ufrag),
			})
		}
	}
	if (fragment.ufrag == "") != (fragment.pwd == "") {
		return whepICEFragment{}, errors.New("ICE fragment credentials are incomplete")
	}
	if len(fragment.candidates) == 0 && fragment.ufrag == "" {
		return whepICEFragment{}, errors.New("ICE fragment has no candidates or credentials")
	}
	return fragment, nil
}

func mergeICECredentials(fragment *whepICEFragment, values []sdp.Attribute) error {
	ufrag, hasUfrag := attribute(values, "ice-ufrag")
	pwd, hasPwd := attribute(values, "ice-pwd")
	if hasUfrag != hasPwd {
		return errors.New("ICE fragment credentials are incomplete")
	}
	if !hasUfrag {
		return nil
	}
	ufrag = strings.TrimSpace(ufrag)
	pwd = strings.TrimSpace(pwd)
	if ufrag == "" || pwd == "" {
		return errors.New("ICE fragment credentials are empty")
	}
	if fragment.ufrag != "" && (fragment.ufrag != ufrag || fragment.pwd != pwd) {
		return errors.New("ICE fragment contains conflicting credentials")
	}
	fragment.ufrag = ufrag
	fragment.pwd = pwd
	return nil
}

func iceCredentials(raw string) (string, string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", "", err
	}
	fragment := whepICEFragment{}
	if err := mergeICECredentials(&fragment, description.Attributes); err != nil {
		return "", "", err
	}
	for _, media := range description.MediaDescriptions {
		if media != nil {
			if err := mergeICECredentials(&fragment, media.Attributes); err != nil {
				return "", "", err
			}
		}
	}
	if fragment.ufrag == "" || fragment.pwd == "" {
		return "", "", errors.New("SDP has no ICE credentials")
	}
	return fragment.ufrag, fragment.pwd, nil
}

func replaceICECredentials(raw string, ufrag string, pwd string) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", err
	}
	replaced := replaceICEAttributes(&description.Attributes, ufrag, pwd)
	for _, media := range description.MediaDescriptions {
		if media != nil {
			replaced = replaceICEAttributes(&media.Attributes, ufrag, pwd) || replaced
		}
	}
	if !replaced {
		return "", errors.New("SDP has no ICE credentials")
	}
	encoded, err := description.Marshal()
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func replaceICEAttributes(values *[]sdp.Attribute, ufrag string, pwd string) bool {
	replaced := false
	filtered := (*values)[:0]
	for _, value := range *values {
		switch value.Key {
		case "ice-ufrag":
			value.Value = ufrag
			replaced = true
		case "ice-pwd":
			value.Value = pwd
			replaced = true
		case "candidate", "end-of-candidates", "remote-candidates":
			continue
		}
		filtered = append(filtered, value)
	}
	*values = filtered
	return replaced
}

func iceFragmentFromAnswer(raw string) (string, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return "", err
	}
	var out strings.Builder
	appendSelectedAttributes(&out, description.Attributes, map[string]bool{
		"group": true, "ice-lite": true, "ice-options": true, "ice-pacing": true, "ice-ufrag": true, "ice-pwd": true,
	})
	mediaCount := 0
	for _, media := range description.MediaDescriptions {
		if media == nil || !hasICEAttributes(media.Attributes) {
			continue
		}
		out.WriteString("m=")
		out.WriteString(media.MediaName.String())
		out.WriteString("\r\n")
		appendSelectedAttributes(&out, media.Attributes, map[string]bool{
			"mid": true, "ice-ufrag": true, "ice-pwd": true, "candidate": true, "end-of-candidates": true,
		})
		mediaCount++
	}
	if mediaCount == 0 {
		return "", errors.New("ICE restart answer has no media candidates")
	}
	return out.String(), nil
}

func hasICEAttributes(values []sdp.Attribute) bool {
	_, hasUfrag := attribute(values, "ice-ufrag")
	_, hasCandidate := attribute(values, "candidate")
	return hasUfrag || hasCandidate
}

func appendSelectedAttributes(out *strings.Builder, values []sdp.Attribute, selected map[string]bool) {
	for _, value := range values {
		if !selected[value.Key] {
			continue
		}
		out.WriteString("a=")
		out.WriteString(value.Key)
		if value.Value != "" {
			out.WriteByte(':')
			out.WriteString(value.Value)
		}
		out.WriteString("\r\n")
	}
}

func attribute(values []sdp.Attribute, key string) (string, bool) {
	for _, value := range values {
		if value.Key == key {
			return value.Value, true
		}
	}
	return "", false
}

func hasAttribute(values []sdp.Attribute, key string) bool {
	_, ok := attribute(values, key)
	return ok
}

func attributes(values []sdp.Attribute, key string) []string {
	out := make([]string, 0, 4)
	for _, value := range values {
		if value.Key == key {
			out = append(out, value.Value)
		}
	}
	return out
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func normalizeSDPLines(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	return strings.ReplaceAll(strings.TrimSpace(raw), "\n", "\r\n") + "\r\n"
}
