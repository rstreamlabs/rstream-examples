//go:build integration && qualification

package bridge

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/whipwhep"
)

const (
	qualificationBitrate       = 8_000_000
	qualificationFramesPerSec  = 30
	qualificationPayloadLimit  = 1_188
	qualificationPhaseDuration = 5 * time.Second
	qualificationChurnViewers  = 12
	qualificationReaderLimit   = mediaMTXTestReaderLimit
)

// qualificationH264Base64 contains one second of deterministic, 30 fps,
// constrained-baseline H264 generated without B-frames. Keeping a real GOP in
// the fixture makes MediaMTX validate the same decode-order semantics as a
// production encoder instead of accepting arbitrary bytes labelled as IDRs.
//
//go:embed testdata/qualification-baseline-30fps.h264.b64
var qualificationH264Base64 string

var qualificationAccessUnitDelimiter = []byte{0x00, 0x00, 0x00, 0x01, 0x09}

type qualificationViewer struct {
	peer         *webrtc.PeerConnection
	session      *whipwhep.Session
	packets      atomic.Uint64
	shortPackets atomic.Uint64
	payloadBytes atomic.Uint64
}

type qualificationPhase struct {
	Readers              int       `json:"readers"`
	DurationMilliseconds int64     `json:"durationMilliseconds"`
	SourcePackets        uint64    `json:"sourcePackets"`
	SourcePayloadBytes   uint64    `json:"sourcePayloadBytes"`
	InboundBytes         uint64    `json:"inboundBytes"`
	OutboundBytes        uint64    `json:"outboundBytes"`
	ViewerPackets        []uint64  `json:"viewerPackets"`
	ViewerShortPackets   []uint64  `json:"viewerShortPackets"`
	ViewerPayloadBytes   []uint64  `json:"viewerPayloadBytes"`
	ViewerPayloadRatio   []float64 `json:"viewerPayloadRatio"`
	InboundBitsPerSec    float64   `json:"inboundBitsPerSecond"`
	OutboundBitsPerSec   float64   `json:"outboundBitsPerSecond"`
}

type qualificationProcess struct {
	PeakResidentBytes          uint64  `json:"peakResidentBytes"`
	CPUSeconds                 float64 `json:"cpuSeconds"`
	CPUCoreRatio               float64 `json:"cpuCoreRatio"`
	PeakProcesses              int     `json:"peakProcesses"`
	SampleDurationMilliseconds int64   `json:"sampleDurationMilliseconds"`
}

type qualificationEvidence struct {
	Revision                       string               `json:"revision"`
	GeneratedAt                    string               `json:"generatedAt"`
	GOOS                           string               `json:"goos"`
	GOARCH                         string               `json:"goarch"`
	LogicalCPUs                    int                  `json:"logicalCPUs"`
	MediaMTXVersion                string               `json:"mediaMTXVersion"`
	TargetBitsPerSecond            int                  `json:"targetBitsPerSecond"`
	FramesPerSecond                int                  `json:"framesPerSecond"`
	FirstViewerSetupMilliseconds   int64                `json:"firstViewerSetupMilliseconds"`
	WarmViewerSetupMilliseconds    []int64              `json:"warmViewerSetupMilliseconds"`
	WarmViewerSetupP95Milliseconds int64                `json:"warmViewerSetupP95Milliseconds"`
	ChurnViewerCount               int                  `json:"churnViewerCount"`
	ChurnSetupP95Milliseconds      int64                `json:"churnSetupP95Milliseconds"`
	ReaderLimit                    int                  `json:"readerLimit"`
	SaturationRejected             bool                 `json:"saturationRejected"`
	SaturationRejectMilliseconds   int64                `json:"saturationRejectMilliseconds"`
	SaturationViewerPayloadRatio   []float64            `json:"saturationViewerPayloadRatio"`
	SourceSessions                 uint32               `json:"sourceSessions"`
	SourceDeletes                  uint32               `json:"sourceDeletes"`
	SourceTWCCPackets              uint32               `json:"sourceTWCCPackets"`
	Phases                         []qualificationPhase `json:"phases"`
	Process                        qualificationProcess `json:"process"`
	Gates                          map[string]bool      `json:"gates"`
	Failures                       []string             `json:"failures"`
	Passed                         bool                 `json:"passed"`
}

type pathCounters struct {
	readers  int
	inbound  uint64
	outbound uint64
}

type processPoint struct {
	cpuSeconds float64
	rssBytes   uint64
	processes  int
}

type processTreeSampler struct {
	root      int
	mu        sync.Mutex
	first     processPoint
	last      processPoint
	peakRSS   uint64
	peakCount int
	started   bool
	startedAt time.Time
	cancel    context.CancelFunc
	done      chan struct{}
	sampleErr error
}

type processRecord struct {
	pid        int
	parent     int
	rssBytes   uint64
	cpuSeconds float64
}

func TestMediaMTXFanOutQualification(t *testing.T) {
	output := strings.TrimSpace(os.Getenv("RSTREAM_DISTRIBUTOR_QUALIFICATION_OUTPUT"))
	if output == "" {
		t.Skip("RSTREAM_DISTRIBUTOR_QUALIFICATION_OUTPUT is not configured")
	}
	revision := strings.TrimSpace(os.Getenv("RSTREAM_DISTRIBUTOR_QUALIFICATION_REVISION"))
	if revision == "" {
		t.Fatal("RSTREAM_DISTRIBUTOR_QUALIFICATION_REVISION is required")
	}
	mediaMTX := mediaMTXExecutable(t)
	version := mediaMTXVersion(t, mediaMTX)
	source := newSourceHarness(t)
	server := httptestServer(t, source)
	defer server.Close()
	process, logs := startMediaMTX(t, mediaMTX, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	sampler := startProcessTreeSampler(t, process.Process.Pid)
	defer sampler.cancel()
	started := time.Now()
	first := openQualificationViewer(t)
	firstSetup := time.Since(started)
	viewers := []*qualificationViewer{first}
	defer func() { closeQualificationViewers(viewers) }()
	waitSignal(t, source.connected, "qualification source peer connection")
	source.stopWarmup()
	waitSignal(t, source.warmupDone, "qualification source warmup shutdown")
	nextSequence := source.nextSequence.Load()
	if nextSequence > 65535 {
		t.Fatalf("qualification source sequence %d exceeds RTP sequence space", nextSequence)
	}
	warmSetups := make([]time.Duration, 0, 7)
	phases := make([]qualificationPhase, 0, 3)
	sequence := uint16(nextSequence)
	timestamp := nextSequence * (90_000 / qualificationFramesPerSec)
	phase := runQualificationPhase(t, source, viewers, &sequence, &timestamp)
	phases = append(phases, phase)
	for len(viewers) < 4 {
		viewerStarted := time.Now()
		viewers = append(viewers, openQualificationViewer(t))
		warmSetups = append(warmSetups, time.Since(viewerStarted))
	}
	phases = append(phases, runQualificationPhase(t, source, viewers, &sequence, &timestamp))
	for len(viewers) < qualificationReaderLimit {
		viewerStarted := time.Now()
		viewers = append(viewers, openQualificationViewer(t))
		warmSetups = append(warmSetups, time.Since(viewerStarted))
	}
	phases = append(phases, runQualificationPhase(t, source, viewers, &sequence, &timestamp))
	saturationStarted := time.Now()
	saturationRejected := rejectQualificationViewer(t)
	saturationDuration := time.Since(saturationStarted)
	waitPathReaders(t, qualificationReaderLimit)
	saturationRatios := runQualificationContinuity(t, source, viewers, &sequence, &timestamp)
	churnSetups := make([]time.Duration, 0, qualificationChurnViewers)
	for replacement := range qualificationChurnViewers {
		index := replacement % len(viewers)
		viewers[index].close()
		waitPathReaders(t, qualificationReaderLimit-1)
		viewerStarted := time.Now()
		viewers[index] = openQualificationViewer(t)
		churnSetups = append(churnSetups, time.Since(viewerStarted))
	}
	waitCounter(t, &source.twcc, "qualification source TWCC feedback")
	processStats := sampler.stop(t)
	closeQualificationViewers(viewers)
	waitSignal(t, source.deleted, "qualification source WHEP DELETE")
	evidence := buildQualificationEvidence(revision, version, firstSetup, warmSetups, churnSetups, source, phases, processStats, logs.String(), saturationRejected, saturationDuration, saturationRatios)
	writeQualificationEvidence(t, output, evidence)
	if !evidence.Passed {
		t.Fatalf("fan-out qualification failed: %s", strings.Join(evidence.Failures, "; "))
	}
}

func httptestServer(t *testing.T, source *sourceHarness) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(source.serveHTTP))
}

func openQualificationViewer(t *testing.T) *qualificationViewer {
	t.Helper()
	peer, err := media.NewSourcePeer(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create qualification viewer peer: %v", err)
	}
	viewer := &qualificationViewer{peer: peer}
	connected := make(chan struct{}, 1)
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				packet, _, readErr := track.ReadRTP()
				if readErr != nil {
					return
				}
				if len(packet.Payload) <= 3 {
					viewer.shortPackets.Add(1)
				}
				viewer.packets.Add(1)
				viewer.payloadBytes.Add(uint64(len(packet.Payload)))
			}
		}()
	})
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		_ = peer.Close()
		t.Fatalf("add qualification viewer transceiver: %v", err)
	}
	endpoint, err := url.Parse("http://" + mediaMTXHTTPAddress + "/camera/whep")
	if err != nil {
		_ = peer.Close()
		t.Fatalf("parse qualification viewer endpoint: %v", err)
	}
	session, err := whipwhep.Exchange(context.Background(), peer, endpoint, "", &http.Client{Timeout: 15 * time.Second}, whipwhep.Options{AllowLegacyWildcardETag: true})
	if err != nil {
		_ = peer.Close()
		t.Fatalf("open qualification viewer WHEP session: %v", err)
	}
	viewer.session = session
	if peer.ConnectionState() != webrtc.PeerConnectionStateConnected {
		select {
		case <-connected:
		case <-time.After(5 * time.Second):
			viewer.close()
			t.Fatalf("qualification viewer peer state = %s", peer.ConnectionState())
		}
	}
	remote := peer.RemoteDescription()
	if remote == nil || !strings.Contains(remote.SDP, " transport-cc") || !strings.Contains(remote.SDP, " nack") {
		viewer.close()
		t.Fatal("qualification viewer did not negotiate downstream TWCC and NACK")
	}
	return viewer
}

func rejectQualificationViewer(t *testing.T) bool {
	t.Helper()
	peer, err := media.NewSourcePeer(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create saturated qualification viewer peer: %v", err)
	}
	defer func() { _ = peer.Close() }()
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("add saturated qualification viewer transceiver: %v", err)
	}
	endpoint, err := url.Parse("http://" + mediaMTXHTTPAddress + "/camera/whep")
	if err != nil {
		t.Fatalf("parse saturated qualification viewer endpoint: %v", err)
	}
	session, exchangeErr := whipwhep.Exchange(context.Background(), peer, endpoint, "", &http.Client{Timeout: 15 * time.Second}, whipwhep.Options{AllowLegacyWildcardETag: true})
	if session != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = session.Close(ctx)
		cancel()
	}
	var statusErr *whipwhep.HTTPStatusError
	return exchangeErr != nil &&
		whipwhep.IsPermanent(exchangeErr) &&
		errors.As(exchangeErr, &statusErr) &&
		statusErr.StatusCode == http.StatusBadRequest
}

func (v *qualificationViewer) close() {
	if v == nil || v.session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = v.session.Close(ctx)
	cancel()
	v.session = nil
	_ = v.peer.Close()
}

func closeQualificationViewers(viewers []*qualificationViewer) {
	for index := len(viewers) - 1; index >= 0; index-- {
		viewers[index].close()
	}
}

func runQualificationPhase(t *testing.T, source *sourceHarness, viewers []*qualificationViewer, sequence *uint16, timestamp *uint32) qualificationPhase {
	t.Helper()
	waitPathReaders(t, len(viewers))
	time.Sleep(250 * time.Millisecond)
	beforePath := readPathCounters(t)
	beforePackets := make([]uint64, len(viewers))
	beforeProbes := make([]uint64, len(viewers))
	beforeBytes := make([]uint64, len(viewers))
	for index, viewer := range viewers {
		beforePackets[index] = viewer.packets.Load()
		beforeProbes[index] = viewer.shortPackets.Load()
		beforeBytes[index] = viewer.payloadBytes.Load()
	}
	started := time.Now()
	sourcePackets, sourceBytes := writeQualificationTraffic(t, source.track, sequence, timestamp, qualificationPhaseDuration)
	elapsed := time.Since(started)
	time.Sleep(750 * time.Millisecond)
	afterPath := readPathCounters(t)
	phase := qualificationPhase{
		Readers:              len(viewers),
		DurationMilliseconds: elapsed.Milliseconds(),
		SourcePackets:        sourcePackets,
		SourcePayloadBytes:   sourceBytes,
		InboundBytes:         afterPath.inbound - beforePath.inbound,
		OutboundBytes:        afterPath.outbound - beforePath.outbound,
		ViewerPackets:        make([]uint64, len(viewers)),
		ViewerShortPackets:   make([]uint64, len(viewers)),
		ViewerPayloadBytes:   make([]uint64, len(viewers)),
		ViewerPayloadRatio:   make([]float64, len(viewers)),
		InboundBitsPerSec:    float64(afterPath.inbound-beforePath.inbound) * 8 / elapsed.Seconds(),
		OutboundBitsPerSec:   float64(afterPath.outbound-beforePath.outbound) * 8 / elapsed.Seconds(),
	}
	for index, viewer := range viewers {
		phase.ViewerPackets[index] = viewer.packets.Load() - beforePackets[index]
		phase.ViewerShortPackets[index] = viewer.shortPackets.Load() - beforeProbes[index]
		phase.ViewerPayloadBytes[index] = viewer.payloadBytes.Load() - beforeBytes[index]
		phase.ViewerPayloadRatio[index] = float64(phase.ViewerPayloadBytes[index]) / float64(sourceBytes)
	}
	return phase
}

func runQualificationContinuity(t *testing.T, source *sourceHarness, viewers []*qualificationViewer, sequence *uint16, timestamp *uint32) []float64 {
	t.Helper()
	beforeBytes := make([]uint64, len(viewers))
	for index, viewer := range viewers {
		beforeBytes[index] = viewer.payloadBytes.Load()
	}
	_, sourceBytes := writeQualificationTraffic(t, source.track, sequence, timestamp, time.Second)
	time.Sleep(750 * time.Millisecond)
	ratios := make([]float64, len(viewers))
	for index, viewer := range viewers {
		delivered := viewer.payloadBytes.Load() - beforeBytes[index]
		ratios[index] = float64(delivered) / float64(sourceBytes)
	}
	return ratios
}

func writeQualificationTraffic(t *testing.T, track *webrtc.TrackLocalStaticRTP, sequence *uint16, timestamp *uint32, duration time.Duration) (uint64, uint64) {
	t.Helper()
	frames := int(duration.Seconds() * qualificationFramesPerSec)
	payloadPerFrame := qualificationBitrate / 8 / qualificationFramesPerSec
	accessUnits := qualificationAccessUnits(t, payloadPerFrame)
	payloader := &codecs.H264Payloader{}
	started := time.Now()
	var packets uint64
	var payloadBytes uint64
	for frame := range frames {
		accessUnit := accessUnits[frame%len(accessUnits)]
		payloads := payloader.Payload(qualificationPayloadLimit, accessUnit)
		if len(payloads) == 0 {
			t.Fatal("H264 payloader produced no qualification packets")
		}
		for packetIndex, payload := range payloads {
			packet := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: media.PrimaryPayloadType, SequenceNumber: *sequence, Timestamp: *timestamp, Marker: packetIndex == len(payloads)-1}, Payload: payload}
			if err := track.WriteRTP(packet); err != nil {
				t.Fatalf("write qualification packet %d: %v", *sequence, err)
			}
			(*sequence)++
			packets++
			payloadBytes += uint64(len(payload))
		}
		*timestamp += 90_000 / qualificationFramesPerSec
		deadline := started.Add(time.Duration(frame+1) * time.Second / qualificationFramesPerSec)
		if delay := time.Until(deadline); delay > 0 {
			time.Sleep(delay)
		}
	}
	return packets, payloadBytes
}

func qualificationAccessUnits(t *testing.T, targetSize int) [][]byte {
	t.Helper()
	stream, err := base64.StdEncoding.DecodeString(qualificationH264Base64)
	if err != nil {
		t.Fatalf("decode qualification H264 fixture: %v", err)
	}
	starts := make([]int, 0, qualificationFramesPerSec)
	for offset := 0; ; {
		index := bytes.Index(stream[offset:], qualificationAccessUnitDelimiter)
		if index < 0 {
			break
		}
		starts = append(starts, offset+index)
		offset += index + len(qualificationAccessUnitDelimiter)
	}
	if len(starts) != qualificationFramesPerSec {
		t.Fatalf("qualification H264 fixture contains %d access units, want %d", len(starts), qualificationFramesPerSec)
	}
	accessUnits := make([][]byte, 0, len(starts))
	for index, start := range starts {
		end := len(stream)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		accessUnit := append([]byte(nil), stream[start:end]...)
		accessUnits = append(accessUnits, appendQualificationSEI(t, accessUnit, targetSize))
	}
	return accessUnits
}

func appendQualificationSEI(t *testing.T, accessUnit []byte, targetSize int) []byte {
	t.Helper()
	const minimumPayload = 16
	remaining := targetSize - len(accessUnit)
	payloadSize := remaining
	for payloadSize >= minimumPayload && qualificationSEISize(payloadSize) > remaining {
		payloadSize--
	}
	if payloadSize < minimumPayload {
		t.Fatalf("qualification access unit of %d bytes cannot fit the %d-byte target", len(accessUnit), targetSize)
	}
	sei := make([]byte, 0, qualificationSEISize(payloadSize))
	sei = append(sei, 0x00, 0x00, 0x00, 0x01, 0x06, 0x05)
	remainingPayloadSize := payloadSize
	for remainingPayloadSize >= 255 {
		sei = append(sei, 0xff)
		remainingPayloadSize -= 255
	}
	sei = append(sei, byte(remainingPayloadSize))
	for range payloadSize {
		sei = append(sei, 0xa5)
	}
	sei = append(sei, 0x80)
	return append(accessUnit, sei...)
}

func qualificationSEISize(payloadSize int) int {
	return 8 + payloadSize/255 + payloadSize
}

func waitPathReaders(t *testing.T, readers int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		counters, err := fetchPathCounters()
		if err == nil && counters.readers == readers {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("MediaMTX reader count did not reach %d", readers)
}

func readPathCounters(t *testing.T) pathCounters {
	t.Helper()
	counters, err := fetchPathCounters()
	if err != nil {
		t.Fatalf("read MediaMTX path counters: %v", err)
	}
	return counters
}

func fetchPathCounters() (pathCounters, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + mediaMTXMetricsAddress + "/metrics?type=paths&path=camera")
	if err != nil {
		return pathCounters{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil {
		return pathCounters{}, err
	}
	if response.StatusCode != http.StatusOK || len(body) > 256*1024 {
		return pathCounters{}, fmt.Errorf("metrics response = %d and %d bytes", response.StatusCode, len(body))
	}
	text := string(body)
	readers, err := metricUint(text, "paths_readers")
	if err != nil {
		return pathCounters{}, err
	}
	inbound, err := metricUint(text, "paths_inbound_bytes")
	if err != nil {
		return pathCounters{}, err
	}
	outbound, err := metricUint(text, "paths_outbound_bytes")
	if err != nil {
		return pathCounters{}, err
	}
	return pathCounters{readers: int(readers), inbound: inbound, outbound: outbound}, nil
}

func metricUint(text string, name string) (uint64, error) {
	line := metricLine(text, name)
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return 0, fmt.Errorf("metric %s is unavailable", name)
	}
	value, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse metric %s: %w", name, err)
	}
	return value, nil
}

func startProcessTreeSampler(t *testing.T, root int) *processTreeSampler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sampler := &processTreeSampler{root: root, cancel: cancel, done: make(chan struct{}), startedAt: time.Now()}
	if err := sampler.sample(ctx); err != nil {
		cancel()
		t.Fatalf("sample MediaMTX process tree: %v", err)
	}
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sampler.sample(ctx); err != nil && ctx.Err() == nil {
					sampler.mu.Lock()
					if sampler.sampleErr == nil {
						sampler.sampleErr = err
					}
					sampler.mu.Unlock()
				}
			}
		}
	}()
	return sampler
}

func (s *processTreeSampler) sample(ctx context.Context) error {
	point, err := readProcessTree(ctx, s.root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.first = point
		s.started = true
	}
	s.last = point
	if point.rssBytes > s.peakRSS {
		s.peakRSS = point.rssBytes
	}
	if point.processes > s.peakCount {
		s.peakCount = point.processes
	}
	return nil
}

func (s *processTreeSampler) stop(t *testing.T) qualificationProcess {
	t.Helper()
	if err := s.sample(context.Background()); err != nil {
		t.Fatalf("sample final MediaMTX process tree: %v", err)
	}
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("MediaMTX process sampler did not stop")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sampleErr != nil {
		t.Fatalf("MediaMTX process sampler failed: %v", s.sampleErr)
	}
	cpu := s.last.cpuSeconds - s.first.cpuSeconds
	if cpu < 0 {
		t.Fatal("MediaMTX process CPU counter regressed")
	}
	duration := time.Since(s.startedAt)
	return qualificationProcess{PeakResidentBytes: s.peakRSS, CPUSeconds: cpu, CPUCoreRatio: cpu / duration.Seconds(), PeakProcesses: s.peakCount, SampleDurationMilliseconds: duration.Milliseconds()}
}

func readProcessTree(ctx context.Context, root int) (processPoint, error) {
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,rss=,time=").Output()
	if err != nil {
		return processPoint{}, fmt.Errorf("ps: %w", err)
	}
	records := make(map[int]processRecord)
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		rssKB, rssErr := strconv.ParseUint(fields[2], 10, 64)
		cpu, cpuErr := parseProcessCPU(fields[3])
		if pidErr != nil || parentErr != nil || rssErr != nil || cpuErr != nil {
			continue
		}
		records[pid] = processRecord{pid: pid, parent: parent, rssBytes: rssKB * 1024, cpuSeconds: cpu}
	}
	if _, exists := records[root]; !exists {
		return processPoint{}, fmt.Errorf("root process %d is absent", root)
	}
	selected := map[int]struct{}{root: {}}
	changed := true
	for changed {
		changed = false
		for pid, record := range records {
			if _, exists := selected[pid]; exists {
				continue
			}
			if _, exists := selected[record.parent]; exists {
				selected[pid] = struct{}{}
				changed = true
			}
		}
	}
	point := processPoint{processes: len(selected)}
	for pid := range selected {
		record := records[pid]
		point.rssBytes += record.rssBytes
		point.cpuSeconds += record.cpuSeconds
	}
	return point, nil
}

func parseProcessCPU(raw string) (float64, error) {
	days := 0
	timePart := raw
	if dash := strings.IndexByte(raw, '-'); dash >= 0 {
		value, err := strconv.Atoi(raw[:dash])
		if err != nil {
			return 0, err
		}
		days = value
		timePart = raw[dash+1:]
	}
	parts := strings.Split(timePart, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid process CPU time %q", raw)
	}
	seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil {
		return 0, err
	}
	hours := 0
	if len(parts) == 3 {
		hours, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, err
		}
	}
	return float64(days*86_400+hours*3_600+minutes*60) + seconds, nil
}

func mediaMTXVersion(t *testing.T, binary string) string {
	t.Helper()
	output, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("read MediaMTX version: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func buildQualificationEvidence(revision string, version string, firstSetup time.Duration, warmSetups []time.Duration, churnSetups []time.Duration, source *sourceHarness, phases []qualificationPhase, process qualificationProcess, runtimeLogs string, saturationRejected bool, saturationDuration time.Duration, saturationRatios []float64) qualificationEvidence {
	warmMilliseconds := durationMilliseconds(warmSetups)
	churnMilliseconds := durationMilliseconds(churnSetups)
	evidence := qualificationEvidence{
		Revision:                       revision,
		GeneratedAt:                    time.Now().UTC().Format(time.RFC3339),
		GOOS:                           runtime.GOOS,
		GOARCH:                         runtime.GOARCH,
		LogicalCPUs:                    runtime.NumCPU(),
		MediaMTXVersion:                version,
		TargetBitsPerSecond:            qualificationBitrate,
		FramesPerSecond:                qualificationFramesPerSec,
		FirstViewerSetupMilliseconds:   firstSetup.Milliseconds(),
		WarmViewerSetupMilliseconds:    warmMilliseconds,
		WarmViewerSetupP95Milliseconds: percentile95(warmMilliseconds),
		ChurnViewerCount:               len(churnSetups),
		ChurnSetupP95Milliseconds:      percentile95(churnMilliseconds),
		ReaderLimit:                    qualificationReaderLimit,
		SaturationRejected:             saturationRejected,
		SaturationRejectMilliseconds:   saturationDuration.Milliseconds(),
		SaturationViewerPayloadRatio:   saturationRatios,
		SourceSessions:                 source.posts.Load(),
		SourceDeletes:                  source.deletes.Load(),
		SourceTWCCPackets:              source.twcc.Load(),
		Phases:                         phases,
		Process:                        process,
		Gates:                          make(map[string]bool),
	}
	evidence.Gates["one-source-session"] = evidence.SourceSessions == 1
	evidence.Gates["one-source-delete"] = evidence.SourceDeletes == 1
	evidence.Gates["source-twcc"] = evidence.SourceTWCCPackets > 0
	evidence.Gates["first-viewer-start"] = evidence.FirstViewerSetupMilliseconds <= 5_000
	evidence.Gates["warm-viewer-start"] = evidence.WarmViewerSetupP95Milliseconds <= 2_000
	evidence.Gates["churn-viewer-start"] = evidence.ChurnSetupP95Milliseconds <= 2_000
	evidence.Gates["reader-limit-rejected"] = evidence.SaturationRejected
	evidence.Gates["reader-limit-fast-rejection"] = evidence.SaturationRejectMilliseconds <= 2_000
	evidence.Gates["reader-limit-existing-viewers"] = len(evidence.SaturationViewerPayloadRatio) == qualificationReaderLimit
	for _, ratio := range evidence.SaturationViewerPayloadRatio {
		if ratio < 0.99 || ratio > 1.02 {
			evidence.Gates["reader-limit-existing-viewers"] = false
			break
		}
	}
	evidence.Gates["bounded-memory"] = evidence.Process.PeakResidentBytes <= 256*1024*1024
	evidence.Gates["bounded-cpu"] = evidence.Process.CPUCoreRatio <= 2
	evidence.Gates["process-topology"] = evidence.Process.PeakProcesses >= 2 && evidence.Process.PeakProcesses <= 3
	evidence.Gates["phase-count"] = len(phases) == 3 && phases[0].Readers == 1 && phases[1].Readers == 4 && phases[2].Readers == 8
	evidence.Gates["valid-h264-runtime"] = qualificationRuntimeHealthy(runtimeLogs)
	for _, phase := range phases {
		for _, ratio := range phase.ViewerPayloadRatio {
			if ratio < 0.99 || ratio > 1.02 {
				evidence.Gates["viewer-media-volume"] = false
				break
			}
		}
		if _, exists := evidence.Gates["viewer-media-volume"]; !exists {
			evidence.Gates["viewer-media-volume"] = true
		}
		for index := 1; index < len(phase.ViewerPackets); index++ {
			if phase.ViewerPackets[index] != phase.ViewerPackets[0] || phase.ViewerPayloadBytes[index] != phase.ViewerPayloadBytes[0] {
				evidence.Gates["viewer-consistency"] = false
				break
			}
		}
		if _, exists := evidence.Gates["viewer-consistency"]; !exists {
			evidence.Gates["viewer-consistency"] = true
		}
		minimumFanout := float64(phase.Readers) * 0.85
		maximumFanout := float64(phase.Readers) * 1.15
		ratio := float64(phase.OutboundBytes) / float64(phase.InboundBytes)
		if ratio < minimumFanout || ratio > maximumFanout {
			evidence.Gates["fanout-byte-scaling"] = false
		}
	}
	if _, exists := evidence.Gates["fanout-byte-scaling"]; !exists {
		evidence.Gates["fanout-byte-scaling"] = true
	}
	minimumInbound := phases[0].InboundBitsPerSec
	maximumInbound := phases[0].InboundBitsPerSec
	for _, phase := range phases[1:] {
		if phase.InboundBitsPerSec < minimumInbound {
			minimumInbound = phase.InboundBitsPerSec
		}
		if phase.InboundBitsPerSec > maximumInbound {
			maximumInbound = phase.InboundBitsPerSec
		}
	}
	evidence.Gates["constant-device-uplink"] = minimumInbound > 0 && maximumInbound/minimumInbound <= 1.10
	gateNames := make([]string, 0, len(evidence.Gates))
	for name := range evidence.Gates {
		gateNames = append(gateNames, name)
	}
	sort.Strings(gateNames)
	for _, name := range gateNames {
		if !evidence.Gates[name] {
			evidence.Failures = append(evidence.Failures, name)
		}
	}
	evidence.Passed = len(evidence.Failures) == 0
	return evidence
}

func qualificationRuntimeHealthy(logs string) bool {
	for _, failure := range []string{
		"doesn't support H264 streams with B-frames",
		"bridge failed:",
		" RTP packets lost",
		"closed: source of path '",
	} {
		if strings.Contains(logs, failure) {
			return false
		}
	}
	return true
}

func TestQualificationRuntimeHealth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		logs string
		want bool
	}{
		{name: "healthy", logs: "stream is available\nbridge stopped: received=100", want: true},
		{name: "B frames", logs: "closed: WebRTC doesn't support H264 streams with B-frames", want: false},
		{name: "bridge failure", logs: "bridge failed: incompatible SDP", want: false},
		{name: "RTP loss", logs: "WAR [session] 18 RTP packets lost", want: false},
		{name: "source timeout", logs: "closed: source of path 'camera' has timed out", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := qualificationRuntimeHealthy(test.logs); got != test.want {
				t.Fatalf("qualificationRuntimeHealthy() = %t, want %t", got, test.want)
			}
		})
	}
}

func durationMilliseconds(values []time.Duration) []int64 {
	result := make([]int64, len(values))
	for index, value := range values {
		result[index] = value.Milliseconds()
	}
	return result
}

func percentile95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	index := (95*len(ordered) + 99) / 100
	if index == 0 {
		index = 1
	}
	return ordered[index-1]
}

func writeQualificationEvidence(t *testing.T, output string, evidence qualificationEvidence) {
	t.Helper()
	payload, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatalf("marshal fan-out evidence: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatalf("create fan-out evidence directory: %v", err)
	}
	if err := os.WriteFile(output, append(payload, '\n'), 0o644); err != nil {
		t.Fatalf("write fan-out evidence: %v", err)
	}
}
