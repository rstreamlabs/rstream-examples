//go:build integration

package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/flexfec"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/media"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/distributor/internal/whipwhep"
)

const (
	mediaMTXHTTPAddress     = "127.0.0.1:18889"
	mediaMTXICEAddress      = "127.0.0.1:18189"
	mediaMTXMetricsAddress  = "127.0.0.1:19998"
	mediaMTXTestReaderLimit = 8
)

type sourceHarness struct {
	peer           *webrtc.PeerConnection
	track          *webrtc.TrackLocalStaticRTP
	connected      chan struct{}
	deleted        chan struct{}
	warmupStop     chan struct{}
	warmupDone     chan struct{}
	posts          atomic.Uint32
	deletes        atomic.Uint32
	nacks          atomic.Uint32
	plis           atomic.Uint32
	twcc           atomic.Uint32
	nextSequence   atomic.Uint32
	warmupWrites   atomic.Uint32
	warmupDelay    time.Duration
	once           sync.Once
	warmupStopOnce sync.Once
	offerMu        sync.Mutex
	offer          string
	diagnostics    synchronizedBuffer
}

type viewerHarness struct {
	peer    *webrtc.PeerConnection
	session *whipwhep.Session
	markers chan uint16
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

type sourceRouter struct {
	sources []*sourceHarness
	index   atomic.Uint32
}

type failOnceSource struct {
	target http.Handler
	failed atomic.Bool
}

type rejectedSource struct {
	requests atomic.Uint32
}

type counterOfferSource struct {
	offer   string
	posts   atomic.Uint32
	patches atomic.Uint32
}

func TestMediaMTXRunOnDemandUsesOneBridgeAndRepairsFlexFEC(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	source := newSourceHarness(t)
	server := httptest.NewServer(http.HandlerFunc(source.serveHTTP))
	defer server.Close()
	process, logs := startMediaMTX(t, mediaMTX, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	first := newViewer(t)
	defer first.close()
	second := newViewer(t)
	defer second.close()
	waitSignal(t, source.connected, "source peer connection")
	waitCounter(t, &source.plis, "initial source key-frame request")
	assertProtectedSourceOffer(t, source.offerSDP())
	firstSequence, lastSequence := writeSourcePackets(t, source, 299)
	assertMarkers(t, first.markers, firstSequence, lastSequence)
	assertMarkers(t, second.markers, firstSequence, lastSequence)
	waitCounter(t, &source.twcc, "source TWCC feedback")
	assertMediaMTXPathMetrics(t, 2)
	if source.posts.Load() != 1 {
		t.Fatalf("source WHEP POSTs = %d, want 1 for two viewers", source.posts.Load())
	}
	first.close()
	second.close()
	waitSignal(t, source.deleted, "source WHEP DELETE after the final viewer")
	waitLogContains(t, logs, "repaired_fec=1")
	if source.deletes.Load() != 1 {
		t.Fatalf("source WHEP DELETEs = %d, want 1", source.deletes.Load())
	}
}

func TestMediaMTXRunOnDemandRepairsWithRTXWhenFlexFECIsUnavailable(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	source := newSourceHarnessWithoutFlexFEC(t)
	server := httptest.NewServer(http.HandlerFunc(source.serveHTTP))
	defer server.Close()
	process, logs := startMediaMTXWithOptions(t, mediaMTX, server.URL+"/whep", true)
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	viewer := newViewer(t)
	defer viewer.close()
	waitSignal(t, source.connected, "source peer connection")
	firstSequence, lastSequence := writeSourcePackets(t, source, 99)
	assertMarkers(t, viewer.markers, firstSequence, lastSequence)
	if source.nacks.Load() == 0 {
		t.Fatal("missing source packet did not produce a NACK")
	}
	viewer.close()
	waitSignal(t, source.deleted, "source WHEP DELETE after the viewer")
	waitLogContains(t, logs, "repaired_rtx=1")
}

func TestMediaMTXRunOnDemandWaitsForDelayedSourceBeforePublishing(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	source := newSourceHarnessWithDelay(t, 750*time.Millisecond)
	server := httptest.NewServer(http.HandlerFunc(source.serveHTTP))
	defer server.Close()
	process, logs := startMediaMTX(t, mediaMTX, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	viewer := newViewer(t)
	defer viewer.close()
	waitSignal(t, source.connected, "delayed source peer connection")
	if source.warmupWrites.Load() == 0 {
		t.Fatal("viewer connected before the delayed source published media")
	}
	viewer.close()
	waitSignal(t, source.deleted, "delayed source WHEP DELETE after the viewer")
}

func TestMediaMTXNativeWHEPSourceSharesOneOnDemandSession(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	source := newSourceHarness(t)
	server := httptest.NewServer(http.HandlerFunc(source.serveHTTP))
	defer server.Close()
	sourceURL := "whep://" + strings.TrimPrefix(server.URL, "http://") + "/whep"
	process, logs := startNativeMediaMTX(t, mediaMTX, sourceURL)
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX logs:\n%s", logs.String())
		}
	})
	first := newViewer(t)
	defer first.close()
	second := newViewer(t)
	defer second.close()
	waitSignal(t, source.connected, "native WHEP source peer connection")
	firstSequence, lastSequence := writeSourcePackets(t, source, 99)
	assertMarkers(t, first.markers, firstSequence, lastSequence)
	assertMarkers(t, second.markers, firstSequence, lastSequence)
	if source.posts.Load() != 1 {
		t.Fatalf("native source WHEP POSTs = %d, want 1 for two viewers", source.posts.Load())
	}
	offer := source.offerSDP()
	if !strings.Contains(offer, "a=rtcp-fb:") || !strings.Contains(offer, " nack") {
		t.Fatalf("native source offer did not negotiate NACK:\n%s", offer)
	}
	if !strings.Contains(offer, "transport-wide-cc-extensions") || !strings.Contains(offer, " transport-cc") {
		t.Fatalf("native source offer did not negotiate TWCC:\n%s", offer)
	}
	if strings.Contains(strings.ToLower(offer), "rtx/90000") || strings.Contains(strings.ToLower(offer), "flexfec-03/90000") {
		t.Fatalf("native source repair capability changed; requalify the profile comparison:\n%s", offer)
	}
	if strings.Contains(offer, "a=rtcp-mux-only") || strings.Contains(offer, "a=msid:") {
		t.Fatalf("native WHEP conformance changed; requalify the strict producer profile:\n%s", offer)
	}
	first.close()
	second.close()
	waitSignal(t, source.deleted, "native WHEP source DELETE after the final viewer")
	if source.deletes.Load() != 1 {
		t.Fatalf("native source WHEP DELETEs = %d, want 1", source.deletes.Load())
	}
}

func TestMediaMTXNativeWHEPSourceDoesNotCompleteStrictCounterOffer(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	source := newCounterOfferSource(t)
	server := httptest.NewServer(http.HandlerFunc(source.serveHTTP))
	defer server.Close()
	sourceURL := "whep://" + strings.TrimPrefix(server.URL, "http://") + "/whep"
	process, logs := startNativeMediaMTX(t, mediaMTX, sourceURL)
	defer stopMediaMTX(t, process, logs)
	started := time.Now()
	viewer, err := openViewer()
	if err == nil {
		viewer.close()
		t.Fatal("native MediaMTX unexpectedly completed the strict WHEP counter-offer")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("native counter-offer failure took %s to release the viewer", elapsed)
	}
	if source.posts.Load() != 1 {
		t.Fatalf("native source WHEP POSTs = %d, want 1", source.posts.Load())
	}
	if source.patches.Load() != 0 {
		t.Fatalf("native source counter-offer PATCHes = %d; requalify the native profile", source.patches.Load())
	}
}

func TestMediaMTXRunOnDemandStopsAndRestartsWithoutStaleSource(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	firstSource := newSourceHarness(t)
	secondSource := newSourceHarness(t)
	router := &sourceRouter{sources: []*sourceHarness{firstSource, secondSource}}
	server := httptest.NewServer(http.HandlerFunc(router.serveHTTP))
	defer server.Close()
	process, logs := startMediaMTX(t, mediaMTX, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	firstViewer := newViewer(t)
	waitSignal(t, firstSource.connected, "first source peer connection")
	stopSourceWarmup(t, firstSource)
	firstViewer.close()
	waitSignal(t, firstSource.deleted, "first source WHEP DELETE")
	time.Sleep(time.Second)
	secondViewer := newViewer(t)
	waitSignal(t, secondSource.connected, "second source peer connection")
	firstSequence, lastSequence := writeSourcePackets(t, secondSource, 49)
	assertMarkers(t, secondViewer.markers, firstSequence, lastSequence)
	secondViewer.close()
	waitSignal(t, secondSource.deleted, "second source WHEP DELETE")
	if firstSource.posts.Load() != 1 || secondSource.posts.Load() != 1 {
		t.Fatalf("source WHEP POSTs = %d then %d, want 1 then 1", firstSource.posts.Load(), secondSource.posts.Load())
	}
	if firstSource.deletes.Load() != 1 || secondSource.deletes.Load() != 1 {
		t.Fatalf("source WHEP DELETEs = %d then %d, want 1 then 1", firstSource.deletes.Load(), secondSource.deletes.Load())
	}
}

func TestMediaMTXRunOnDemandRecoversAfterSourceNegotiationFailure(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	source := newSourceHarness(t)
	failing := &failOnceSource{target: http.HandlerFunc(source.serveHTTP)}
	server := httptest.NewServer(failing)
	defer server.Close()
	process, logs := startMediaMTX(t, mediaMTX, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and bridge logs:\n%s", logs.String())
		}
	})
	started := time.Now()
	failedViewer, err := openViewer()
	if err == nil {
		failedViewer.close()
		t.Fatal("first viewer unexpectedly succeeded while the source rejected WHEP")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("failed source took %s to release the reader", elapsed)
	}
	if source.posts.Load() != 0 {
		t.Fatalf("successful source WHEP POSTs after rejected attempt = %d, want 0", source.posts.Load())
	}
	viewer := newViewer(t)
	waitSignal(t, source.connected, "recovered source peer connection")
	firstSequence, lastSequence := writeSourcePackets(t, source, 49)
	assertMarkers(t, viewer.markers, firstSequence, lastSequence)
	viewer.close()
	waitSignal(t, source.deleted, "recovered source WHEP DELETE")
	if source.posts.Load() != 1 || source.deletes.Load() != 1 {
		t.Fatalf("recovered source lifecycle = %d POSTs and %d DELETEs, want 1 and 1", source.posts.Load(), source.deletes.Load())
	}
}

func TestMediaMTXRunOnDemandRetriesTransientFailureInsideOneDistributor(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	distributor := distributorExecutable(t)
	source := newSourceHarness(t)
	failing := &failOnceSource{target: http.HandlerFunc(source.serveHTTP)}
	server := httptest.NewServer(failing)
	defer server.Close()
	process, logs := startMediaMTXWithDistributor(t, mediaMTX, distributor, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and distributor logs:\n%s", logs.String())
		}
	})
	viewer := newViewer(t)
	waitSignal(t, source.connected, "source connection after an in-process retry")
	firstSequence, lastSequence := writeSourcePackets(t, source, 49)
	assertMarkers(t, viewer.markers, firstSequence, lastSequence)
	viewer.close()
	waitSignal(t, source.deleted, "source DELETE after transient recovery")
	if !failing.failed.Load() || source.posts.Load() != 1 {
		t.Fatalf("source attempts = rejected %t successful %d, want true and 1", failing.failed.Load(), source.posts.Load())
	}
	waitLogContains(t, logs, "video distributor attempt failed")
}

func TestMediaMTXRunOnDemandRecoversActiveSourceOnViewerReconnect(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	distributor := distributorExecutable(t)
	firstSource := newSourceHarness(t)
	secondSource := newSourceHarness(t)
	secondSource.nextSequence.Store(30000)
	router := &sourceRouter{sources: []*sourceHarness{firstSource, secondSource}}
	server := httptest.NewServer(http.HandlerFunc(router.serveHTTP))
	defer server.Close()
	process, logs := startMediaMTXWithDistributor(t, mediaMTX, distributor, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("MediaMTX and distributor logs:\n%s", logs.String())
		}
	})
	firstViewer := newViewer(t)
	defer firstViewer.close()
	waitSignal(t, firstSource.connected, "first source peer connection")
	stopSourceWarmup(t, firstSource)
	failedAt := time.Now()
	requireNoError(t, firstSource.peer.Close(), "fail active source peer")
	waitSignal(t, firstSource.deleted, "failed source WHEP DELETE")
	firstViewer.close()
	waitLogContains(t, logs, "runOnDemand command stopped: not needed by anyone")
	secondViewer := newViewer(t)
	defer secondViewer.close()
	waitSignal(t, secondSource.connected, "replacement source peer connection")
	if outage := time.Since(failedAt); outage > 5*time.Second {
		t.Fatalf("active source recovery took %s, want at most 5s", outage)
	}
	firstSequence, lastSequence := writeSourcePackets(t, secondSource, 49)
	assertMarkers(t, secondViewer.markers, firstSequence, lastSequence)
	secondViewer.close()
	waitSignal(t, secondSource.deleted, "replacement source WHEP DELETE")
	if firstSource.posts.Load() != 1 || secondSource.posts.Load() != 1 {
		t.Fatalf("source WHEP POSTs = %d then %d, want 1 then 1", firstSource.posts.Load(), secondSource.posts.Load())
	}
	if firstSource.deletes.Load() != 1 || secondSource.deletes.Load() != 1 {
		t.Fatalf("source WHEP DELETEs = %d then %d, want 1 then 1", firstSource.deletes.Load(), secondSource.deletes.Load())
	}
	waitLogContains(t, logs, "video distributor attempt failed")
}

func TestMediaMTXRunOnDemandDoesNotRetryPermanentAuthorizationFailure(t *testing.T) {
	mediaMTX := mediaMTXExecutable(t)
	distributor := distributorExecutable(t)
	source := &rejectedSource{}
	server := httptest.NewServer(source)
	defer server.Close()
	process, logs := startMediaMTXWithDistributor(t, mediaMTX, distributor, server.URL+"/whep")
	defer stopMediaMTX(t, process, logs)
	if viewer, err := openViewer(); err == nil {
		viewer.close()
		t.Fatal("viewer unexpectedly connected through a rejected source")
	}
	time.Sleep(1500 * time.Millisecond)
	if requests := source.requests.Load(); requests != 1 {
		t.Fatalf("rejected source POSTs = %d, want exactly 1", requests)
	}
	waitLogContains(t, logs, "video distributor failed")
	if strings.Contains(logs.String(), "video distributor attempt failed") {
		t.Fatalf("permanent source failure entered the retry loop:\n%s", logs.String())
	}
}

func newSourceHarness(t *testing.T) *sourceHarness {
	return newSourceHarnessWithFlexFEC(t, true)
}

func newSourceHarnessWithoutFlexFEC(t *testing.T) *sourceHarness {
	return newSourceHarnessWithFlexFEC(t, false)
}

func newSourceHarnessWithDelay(t *testing.T, delay time.Duration) *sourceHarness {
	harness := newSourceHarnessWithFlexFEC(t, true)
	harness.warmupDelay = delay
	return harness
}

func newSourceHarnessWithFlexFEC(t *testing.T, flexFEC bool) *sourceHarness {
	t.Helper()
	peer, track, sender := newSender(t, flexFEC)
	harness := &sourceHarness{
		peer:       peer,
		track:      track,
		connected:  make(chan struct{}),
		deleted:    make(chan struct{}),
		warmupStop: make(chan struct{}),
		warmupDone: make(chan struct{}),
	}
	harness.nextSequence.Store(1)
	go func() {
		for {
			packets, _, err := sender.ReadRTCP()
			if err != nil {
				return
			}
			for _, packet := range packets {
				switch packet.(type) {
				case *rtcp.TransportLayerNack:
					harness.nacks.Add(1)
				case *rtcp.PictureLossIndication:
					harness.plis.Add(1)
				case *rtcp.TransportLayerCC:
					harness.twcc.Add(1)
				}
			}
		}
	}()
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		_, _ = fmt.Fprintf(&harness.diagnostics, "peer state: %s\n", state.String())
		if state == webrtc.PeerConnectionStateConnected {
			harness.once.Do(func() {
				close(harness.connected)
				go harness.writeWarmup()
			})
		}
	})
	t.Cleanup(func() {
		harness.stopWarmup()
		_ = peer.Close()
		if t.Failed() {
			t.Logf("source peer diagnostics:\n%swarmup writes: %d", harness.diagnostics.String(), harness.warmupWrites.Load())
		}
	})
	return harness
}

func (s *sourceHarness) writeWarmup() {
	defer close(s.warmupDone)
	if s.warmupDelay > 0 {
		timer := time.NewTimer(s.warmupDelay)
		defer timer.Stop()
		select {
		case <-s.warmupStop:
			return
		case <-timer.C:
		}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.warmupStop:
			return
		default:
		}
		sequence := uint16(s.nextSequence.Add(1) - 1)
		if err := s.track.WriteRTP(sourcePacket(sequence)); err != nil {
			_, _ = fmt.Fprintf(&s.diagnostics, "warmup write failed: %v\n", err)
			return
		}
		s.warmupWrites.Add(1)
		select {
		case <-s.warmupStop:
			return
		case <-ticker.C:
		}
	}
}

func (s *sourceHarness) stopWarmup() {
	s.warmupStopOnce.Do(func() { close(s.warmupStop) })
}

func newSender(t *testing.T, flexFEC bool) (*webrtc.PeerConnection, *webrtc.TrackLocalStaticRTP, *webrtc.RTPSender) {
	t.Helper()
	mediaEngine := &webrtc.MediaEngine{}
	registerSenderCodec(t, mediaEngine)
	registry := &interceptor.Registry{}
	if flexFEC {
		requireNoError(t, webrtc.ConfigureFlexFEC03(
			webrtc.PayloadType(media.FlexFECPayloadType),
			mediaEngine,
			registry,
			flexfec.NumMediaPackets(5),
			flexfec.NumFECPackets(1),
		), "configure source FlexFEC")
	}
	requireNoError(t, webrtc.ConfigureTWCCHeaderExtensionSender(mediaEngine, registry), "configure source TWCC header")
	requireNoError(t, webrtc.ConfigureNack(mediaEngine, registry), "configure source NACK")
	requireNoError(t, webrtc.ConfigureRTCPReports(registry), "configure source RTCP reports")
	requireNoError(t, webrtc.ConfigureTWCCSender(mediaEngine, registry), "configure source TWCC feedback")
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetIncludeLoopbackCandidate(true)
	settingEngine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settingEngine))
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	requireNoError(t, err, "create source peer")
	track, err := webrtc.NewTrackLocalStaticRTP(media.H264Capability(), "video", "source")
	requireNoError(t, err, "create source track")
	sender, err := peer.AddTrack(track)
	requireNoError(t, err, "add source track")
	return peer, track, sender
}

func newCounterOfferSource(t *testing.T) *counterOfferSource {
	t.Helper()
	peer, _, _ := newSender(t, true)
	offer, err := peer.CreateOffer(nil)
	requireNoError(t, err, "create strict counter-offer")
	gathered := webrtc.GatheringCompletePromise(peer)
	requireNoError(t, peer.SetLocalDescription(offer), "set strict counter-offer")
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("strict counter-offer ICE gathering timed out")
	}
	return &counterOfferSource{offer: peer.LocalDescription().SDP}
}

func registerSenderCodec(t *testing.T, mediaEngine *webrtc.MediaEngine) {
	t.Helper()
	primary := webrtc.RTPCodecParameters{
		RTPCodecCapability: media.H264Capability(),
		PayloadType:        webrtc.PayloadType(media.PrimaryPayloadType),
	}
	requireNoError(t, mediaEngine.RegisterCodec(primary, webrtc.RTPCodecTypeVideo), "register source H264")
	rtx := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeRTX,
			ClockRate:   90000,
			SDPFmtpLine: fmt.Sprintf("apt=%d", media.PrimaryPayloadType),
		},
		PayloadType: webrtc.PayloadType(media.RTXPayloadType),
	}
	requireNoError(t, mediaEngine.RegisterCodec(rtx, webrtc.RTPCodecTypeVideo), "register source RTX")
}

func requireNoError(t *testing.T, err error, operation string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
}

func (s *sourceHarness) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodOptions && request.URL.Path == "/whep" {
		writer.Header().Set("Accept-Post", "application/sdp")
		writer.Header().Set("Access-Control-Allow-Methods", "OPTIONS, POST, PATCH, DELETE")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method == http.MethodDelete && request.URL.Path == "/session" {
		s.deletes.Add(1)
		s.stopWarmup()
		select {
		case <-s.deleted:
		default:
			close(s.deleted)
		}
		_ = s.peer.Close()
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method == http.MethodPatch && request.URL.Path == "/session" {
		if request.Header.Get("If-Match") != `"generation-1"` {
			http.Error(writer, "invalid ICE generation", http.StatusPreconditionFailed)
			return
		}
		fragment, err := io.ReadAll(io.LimitReader(request.Body, 64*1024+1))
		if err != nil || len(fragment) > 64*1024 {
			http.Error(writer, "invalid ICE fragment", http.StatusBadRequest)
			return
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(fragment), "\r\n", "\n"), "\n") {
			if !strings.HasPrefix(line, "a=candidate:") {
				continue
			}
			if err := s.peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: strings.TrimPrefix(line, "a=")}); err != nil {
				http.Error(writer, "invalid ICE candidate", http.StatusBadRequest)
				return
			}
		}
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/whep" {
		http.NotFound(writer, request)
		return
	}
	s.posts.Add(1)
	offer, err := io.ReadAll(io.LimitReader(request.Body, 256*1024+1))
	if err != nil || len(offer) == 0 || len(offer) > 256*1024 {
		http.Error(writer, "invalid SDP offer", http.StatusBadRequest)
		return
	}
	s.offerMu.Lock()
	s.offer = string(offer)
	s.offerMu.Unlock()
	if err := s.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
		http.Error(writer, "invalid SDP offer", http.StatusBadRequest)
		return
	}
	answer, err := s.peer.CreateAnswer(nil)
	if err != nil {
		http.Error(writer, "cannot create SDP answer", http.StatusInternalServerError)
		return
	}
	gathered := webrtc.GatheringCompletePromise(s.peer)
	if err := s.peer.SetLocalDescription(answer); err != nil {
		http.Error(writer, "cannot set SDP answer", http.StatusInternalServerError)
		return
	}
	select {
	case <-gathered:
	case <-request.Context().Done():
		return
	case <-time.After(5 * time.Second):
		http.Error(writer, "ICE gathering timed out", http.StatusGatewayTimeout)
		return
	}
	writer.Header().Set("Content-Type", "application/sdp")
	writer.Header().Set("ETag", `"generation-1"`)
	writer.Header().Set("Location", "/session")
	writer.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(writer, s.peer.LocalDescription().SDP)
}

func (s *sourceHarness) offerSDP() string {
	s.offerMu.Lock()
	defer s.offerMu.Unlock()
	return s.offer
}

func (s *counterOfferSource) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodOptions:
		writer.Header().Set("Accept-Post", "application/sdp")
		writer.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		s.posts.Add(1)
		writer.Header().Set("Content-Type", `application/sdp; valid-until="`+time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)+`"`)
		writer.Header().Set("Location", "/session")
		writer.WriteHeader(http.StatusNotAcceptable)
		_, _ = io.WriteString(writer, s.offer)
	case http.MethodPatch:
		s.patches.Add(1)
		writer.WriteHeader(http.StatusNotImplemented)
	default:
		http.NotFound(writer, request)
	}
}

func (r *sourceRouter) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	index := int(r.index.Load())
	if index >= len(r.sources) {
		http.Error(writer, "no source session available", http.StatusServiceUnavailable)
		return
	}
	r.sources[index].serveHTTP(writer, request)
	if request.Method == http.MethodDelete {
		r.index.CompareAndSwap(uint32(index), uint32(index+1))
	}
}

func (s *failOnceSource) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost && !s.failed.Swap(true) {
		http.Error(writer, "source temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	s.target.ServeHTTP(writer, request)
}

func (s *rejectedSource) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		s.requests.Add(1)
	}
	writer.WriteHeader(http.StatusUnauthorized)
}

func startMediaMTX(t *testing.T, mediaMTX string, sourceURL string) (*exec.Cmd, *synchronizedBuffer) {
	return startMediaMTXWithOptions(t, mediaMTX, sourceURL, false)
}

func startMediaMTXWithOptions(t *testing.T, mediaMTX string, sourceURL string, dropFirstFEC bool) (*exec.Cmd, *synchronizedBuffer) {
	t.Helper()
	runCommand := fmt.Sprintf("%q -test.run=^TestBridgeHelperProcess$ --", os.Args[0])
	return startMediaMTXWithRunCommand(t, mediaMTX, runCommand, sourceURL, dropFirstFEC)
}

func startMediaMTXWithDistributor(t *testing.T, mediaMTX string, distributor string, sourceURL string) (*exec.Cmd, *synchronizedBuffer) {
	t.Helper()
	return startMediaMTXWithRunCommand(t, mediaMTX, fmt.Sprintf("%q", distributor), sourceURL, false)
}

func startMediaMTXWithRunCommand(t *testing.T, mediaMTX string, runCommand string, sourceURL string, dropFirstFEC bool) (*exec.Cmd, *synchronizedBuffer) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "mediamtx.yml")
	contents := fmt.Sprintf(`logLevel: debug
logDestinations: [stdout]
rtsp: false
rtmp: false
hls: false
srt: false
moq: false
playback: false
api: false
metrics: true
metricsAddress: %s
pprof: false
webrtc: true
webrtcAddress: %s
webrtcLocalUDPAddress: %s
webrtcLocalTCPAddress: ""
webrtcIPsFromInterfaces: false
webrtcAdditionalHosts: [127.0.0.1]
webrtcICEServers2: []
webrtcTrackGatherTimeout: 250ms
pathDefaults:
  maxReaders: %d
  runOnDemand: %q
  runOnDemandRestart: false
  runOnDemandStartTimeout: 3s
  runOnDemandCloseAfter: 500ms
paths:
  camera:
`, mediaMTXMetricsAddress, mediaMTXHTTPAddress, mediaMTXICEAddress, mediaMTXTestReaderLimit, runCommand)
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatalf("write MediaMTX config: %v", err)
	}
	logs := &synchronizedBuffer{}
	command := exec.Command(mediaMTX, config)
	command.Env = append(
		os.Environ(),
		"GO_WANT_RSTREAM_BRIDGE=1",
		"MTX_PATH=camera",
		"RSTREAM_MEDIAMTX_URL=http://"+mediaMTXHTTPAddress,
		"RSTREAM_SOURCE_URL="+sourceURL,
	)
	if dropFirstFEC {
		command.Env = append(command.Env, "RSTREAM_BRIDGE_DROP_FIRST_FEC=1")
	}
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		t.Fatalf("start MediaMTX: %v", err)
	}
	waitTCP(t, mediaMTXHTTPAddress, command, logs)
	waitTCP(t, mediaMTXMetricsAddress, command, logs)
	return command, logs
}

func distributorExecutable(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve distributor root: %v", err)
	}
	executable := filepath.Join(t.TempDir(), "rstream-video-distributor")
	command := exec.Command("go", "build", "-o", executable, "./cmd/rstream-video-distributor")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build distributor: %v\n%s", err, output)
	}
	return executable
}

func startNativeMediaMTX(t *testing.T, mediaMTX string, sourceURL string) (*exec.Cmd, *synchronizedBuffer) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "mediamtx.yml")
	contents := fmt.Sprintf(`logLevel: debug
logDestinations: [stdout]
rtsp: false
rtmp: false
hls: false
srt: false
moq: false
playback: false
api: false
metrics: false
pprof: false
webrtc: true
webrtcAddress: %s
webrtcLocalUDPAddress: %s
webrtcLocalTCPAddress: ""
webrtcIPsFromInterfaces: false
webrtcAdditionalHosts: [127.0.0.1]
webrtcICEServers2: []
paths:
  camera:
    source: %q
    sourceOnDemand: true
    sourceOnDemandStartTimeout: 3s
    sourceOnDemandCloseAfter: 500ms
`, mediaMTXHTTPAddress, mediaMTXICEAddress, sourceURL)
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatalf("write native MediaMTX config: %v", err)
	}
	logs := &synchronizedBuffer{}
	command := exec.Command(mediaMTX, config)
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		t.Fatalf("start native MediaMTX: %v", err)
	}
	waitTCP(t, mediaMTXHTTPAddress, command, logs)
	return command, logs
}

func stopMediaMTX(t *testing.T, command *exec.Cmd, logs *synchronizedBuffer) {
	t.Helper()
	if command.ProcessState != nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Errorf("MediaMTX did not stop cleanly:\n%s", logs.String())
	}
}

func newViewer(t *testing.T) *viewerHarness {
	t.Helper()
	viewer, err := openViewer()
	if err != nil {
		t.Fatalf("open MediaMTX WHEP session: %v", err)
	}
	return viewer
}

func openViewer() (*viewerHarness, error) {
	peer, err := media.NewSourcePeer(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("create viewer peer: %w", err)
	}
	markers := make(chan uint16, 2048)
	connected := make(chan struct{}, 1)
	mediaReady := make(chan struct{})
	var mediaReadyOnce sync.Once
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
				if len(packet.Payload) >= 3 {
					markers <- uint16(packet.Payload[1])<<8 | uint16(packet.Payload[2])
					mediaReadyOnce.Do(func() { close(mediaReady) })
				}
			}
		}()
	})
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		_ = peer.Close()
		return nil, fmt.Errorf("add viewer transceiver: %w", err)
	}
	endpoint, _ := url.Parse("http://" + mediaMTXHTTPAddress + "/camera/whep")
	session, err := whipwhep.Exchange(context.Background(), peer, endpoint, "", &http.Client{Timeout: 15 * time.Second}, whipwhep.Options{AllowLegacyWildcardETag: true})
	if err != nil {
		_ = peer.Close()
		return nil, err
	}
	if peer.ConnectionState() != webrtc.PeerConnectionStateConnected {
		select {
		case <-connected:
		case <-time.After(5 * time.Second):
			_ = session.Close(context.Background())
			return nil, fmt.Errorf("viewer peer connection state = %s", peer.ConnectionState())
		}
	}
	select {
	case <-mediaReady:
	case <-time.After(5 * time.Second):
		_ = session.Close(context.Background())
		return nil, errors.New("viewer connected without receiving media")
	}
	remote := peer.RemoteDescription()
	if remote == nil || !strings.Contains(remote.SDP, " transport-cc") || !strings.Contains(remote.SDP, "transport-wide-cc-extensions") {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = session.Close(closeCtx)
		cancel()
		return nil, errors.New("viewer did not negotiate downstream TWCC")
	}
	return &viewerHarness{peer: peer, session: session, markers: markers}, nil
}

func (v *viewerHarness) close() {
	if v.session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = v.session.Close(ctx)
	v.session = nil
}

func stopSourceWarmup(t *testing.T, source *sourceHarness) uint16 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for source.nextSequence.Load() < 8 {
		if time.Now().After(deadline) {
			t.Fatal("source warmup did not produce a complete repair group")
		}
		time.Sleep(time.Millisecond)
	}
	source.stopWarmup()
	select {
	case <-source.warmupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("source warmup did not stop")
	}
	return uint16(source.nextSequence.Load())
}

func writeSourcePackets(t *testing.T, source *sourceHarness, count int) (uint16, uint16) {
	t.Helper()
	if count <= 0 {
		t.Fatalf("source packet count = %d, want a positive value", count)
	}
	first := stopSourceWarmup(t, source)
	lastValue := int(first) + count - 1
	if lastValue > int(^uint16(0)) {
		t.Fatalf("source packet range %d through %d exceeds RTP sequence space", first, lastValue)
	}
	last := uint16(lastValue)
	for value := int(first); value <= lastValue; value++ {
		sequence := uint16(value)
		if err := source.track.WriteRTP(sourcePacket(sequence)); err != nil {
			t.Fatalf("write source packet %d: %v", sequence, err)
		}
		time.Sleep(time.Millisecond)
	}
	return first, last
}

func sourcePacket(sequence uint16) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: media.PrimaryPayloadType, SequenceNumber: sequence, Timestamp: uint32(sequence) * 3000, Marker: true}, Payload: []byte{0x65, byte(sequence >> 8), byte(sequence)}}
}

func assertMarkers(t *testing.T, markers <-chan uint16, first, last uint16) {
	t.Helper()
	wanted := int(last-first) + 1
	received := make(map[uint16]struct{}, wanted)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for len(received) < wanted {
		select {
		case marker := <-markers:
			if marker >= first && marker <= last {
				received[marker] = struct{}{}
			}
		case <-timer.C:
			missing := make([]string, 0)
			for sequence := first; sequence <= last; sequence++ {
				if _, exists := received[sequence]; !exists {
					missing = append(missing, strconv.Itoa(int(sequence)))
				}
			}
			t.Fatalf("received %d/%d source packets; missing %v", len(received), wanted, missing)
		}
	}
}

func assertMediaMTXPathMetrics(t *testing.T, readers int) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + mediaMTXMetricsAddress + "/metrics?type=paths&path=camera")
	if err != nil {
		t.Fatalf("read MediaMTX path metrics: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil {
		t.Fatalf("read MediaMTX path metrics body: %v", err)
	}
	if response.StatusCode != http.StatusOK || len(body) > 256*1024 {
		t.Fatalf("MediaMTX path metrics response = %d and %d bytes", response.StatusCode, len(body))
	}
	text := string(body)
	if !strings.Contains(text, `paths{name="camera",state="ready"} 1`) {
		t.Fatalf("MediaMTX path readiness metric is unavailable:\n%s", text)
	}
	readerMetric := metricLine(text, "paths_readers")
	if !strings.Contains(readerMetric, `name="camera"`) ||
		!strings.Contains(readerMetric, `state="ready"`) ||
		!strings.Contains(readerMetric, `readerType="webRTCSession"`) ||
		!strings.HasSuffix(readerMetric, fmt.Sprintf(" %d", readers)) {
		t.Fatalf("MediaMTX reader metric is unexpected: %q\n%s", readerMetric, text)
	}
	for _, name := range []string{"paths_inbound_bytes", "paths_outbound_bytes"} {
		prefix := name + `{name="camera",state="ready"} `
		start := strings.Index(text, prefix)
		if start < 0 {
			t.Fatalf("MediaMTX metric %s is unavailable:\n%s", name, text)
		}
		line := strings.SplitN(strings.SplitN(text[start:], "\n", 2)[0], " ", 2)
		if len(line) != 2 {
			t.Fatalf("MediaMTX metric %s is malformed:\n%s", name, text)
		}
		value, parseErr := strconv.ParseUint(line[1], 10, 64)
		if parseErr != nil || value == 0 {
			t.Fatalf("MediaMTX metric %s = %q, want a positive byte count", name, line[1])
		}
	}
}

func metricLine(text string, name string) string {
	prefix := name + "{"
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitCounter(t *testing.T, counter *atomic.Uint32, description string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for counter.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertProtectedSourceOffer(t *testing.T, offer string) {
	t.Helper()
	for _, expected := range []string{
		"a=rtcp-mux-only",
		"a=msid:",
		" rtx/90000",
		" flexfec-03/90000",
		" transport-cc",
		"draft-holmer-rmcat-transport-wide-cc-extensions",
	} {
		if !strings.Contains(strings.ToLower(offer), expected) {
			t.Fatalf("protected source offer does not contain %q:\n%s", expected, offer)
		}
	}
}

func waitTCP(t *testing.T, address string, command *exec.Cmd, logs *synchronizedBuffer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		if command.ProcessState != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("MediaMTX did not listen on %s:\n%s", address, logs.String())
}

func waitLogContains(t *testing.T, logs *synchronizedBuffer, expected string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains([]byte(logs.String()), []byte(expected)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("logs do not contain %q:\n%s", expected, logs.String())
}

func requireExecutable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("required executable %s is unavailable", path)
	}
}

func mediaMTXExecutable(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("RSTREAM_MEDIAMTX_BINARY"); configured != "" {
		requireExecutable(t, configured)
		return configured
	}
	path, err := exec.LookPath("mediamtx")
	if err != nil {
		path = "/opt/homebrew/bin/mediamtx"
	}
	requireExecutable(t, path)
	return path
}

func TestBridgeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_RSTREAM_BRIDGE") != "1" {
		return
	}
	configuration, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load bridge configuration: %v\n", err)
		os.Exit(1)
	}
	drop := uint16(3)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	result, err := run(ctx, configuration, runOptions{
		dropMediaSequence: &drop,
		dropFirstFEC:      os.Getenv("RSTREAM_BRIDGE_DROP_FIRST_FEC") == "1",
	})
	_, _ = fmt.Fprintf(
		os.Stderr,
		"bridge stopped: received=%d rtx_received=%d repaired_rtx=%d repaired_fec=%d duplicate_rtx=%d nack_requests=%d expired=%d invalid_fec=%d skipped=%d late=%d\n",
		result.Repair.Received,
		result.Repair.RTXReceived,
		result.Repair.RepairedRTX,
		result.Repair.RepairedFEC,
		result.Repair.DuplicateRTX,
		result.Repair.NACKRequests,
		result.Repair.Expired,
		result.InvalidFEC,
		result.Repair.ReorderSkipped,
		result.Repair.ReorderLate,
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "bridge failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
