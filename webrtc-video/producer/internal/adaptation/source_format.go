package adaptation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/media"
)

const (
	sourceFormatPollInterval = 500 * time.Millisecond
	sourceFormatRetryDelay   = 5 * time.Second
)

// FormatWorker owns the slower source-format loop. It never holds the quality
// policy or bitrate-controller lock across a source transition. One worker and
// one coalesced notification per encoder bound both queued work and goroutines.
type FormatWorker struct {
	source        media.SourceFormatController
	quality       *QualityPolicy
	policy        formatPolicy
	estimate      func() int
	logger        *logs.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	updates       chan struct{}
	done          chan struct{}
	start         sync.Once
	active        atomic.Bool
	requestMu     sync.Mutex
	requestCancel context.CancelFunc
}

func NewFormatWorker(cfg config.SourceFormatConfig, source media.SourceFormatController, quality *QualityPolicy, estimate func() int, logger *logs.Logger) (*FormatWorker, error) {
	if source == nil || logger == nil {
		return nil, errors.New("source format worker requires a controller and logger")
	}
	policy, err := newFormatPolicy(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &FormatWorker{
		source: source, quality: quality, policy: policy, estimate: estimate, logger: logger,
		ctx: ctx, cancel: cancel, updates: make(chan struct{}, 1), done: make(chan struct{}),
	}, nil
}

func (w *FormatWorker) Start() {
	w.start.Do(func() {
		w.active.Store(true)
		go w.run()
	})
}

func (w *FormatWorker) Close() {
	w.cancel()
	w.start.Do(func() { close(w.done) })
	<-w.done
}

func (w *FormatWorker) QualityChanged() {
	w.requestMu.Lock()
	if w.requestCancel != nil {
		w.requestCancel()
	}
	w.requestMu.Unlock()
	select {
	case w.updates <- struct{}{}:
	default:
	}
}

func (w *FormatWorker) Snapshot() media.SourceFormatState {
	state := w.source.Snapshot()
	state.Running = state.Running && w.active.Load()
	return state
}

func (w *FormatWorker) run() {
	defer func() { w.active.Store(false); close(w.done) }()
	ticker := time.NewTicker(sourceFormatPollInterval)
	defer ticker.Stop()
	var retryAfter time.Time
	for {
		if w.ctx.Err() != nil {
			return
		}
		now := time.Now()
		state := w.source.Snapshot()
		profile, manual := w.quality.SourceProfile(w.policy.cfg.Default)
		bitrate := 0
		if w.estimate != nil {
			bitrate = w.estimate()
		}
		if target, change := w.policy.choose(now, bitrate, state.Observed, profile, manual); change && !now.Before(retryAfter) {
			ctx, cancel := context.WithTimeout(w.ctx, w.policy.timings.Transition)
			w.requestMu.Lock()
			w.requestCancel = cancel
			w.requestMu.Unlock()
			// A quality notification may have arrived while the decision was
			// being prepared, before its cancellation function was installed.
			latestProfile, latestManual := w.quality.SourceProfile(w.policy.cfg.Default)
			if latestProfile != profile || latestManual != manual {
				cancel()
			}
			confirmed, err := w.source.ApplyFormat(ctx, target.Format)
			cancel()
			w.requestMu.Lock()
			w.requestCancel = nil
			w.requestMu.Unlock()
			if w.ctx.Err() != nil {
				return
			}
			switch {
			case err == nil && confirmed.Observed == target.Format && !confirmed.Pending:
				w.policy.confirm(time.Now(), confirmed.Observed)
				retryAfter = time.Time{}
			case errors.Is(err, media.ErrFormatBusy), errors.Is(err, context.Canceled):
				// Retry from the latest desired state; never enqueue old profiles.
			case errors.Is(err, media.ErrSourceClosed), errors.Is(err, media.ErrSourceNotRunning):
				return
			default:
				if err == nil {
					err = errors.New("source did not confirm the requested format")
				}
				w.logger.Warn("Source format profile %s was not confirmed: %v", target.ID, err)
				retryAfter = time.Now().Add(sourceFormatRetryDelay)
			}
		}
		select {
		case <-w.ctx.Done():
			return
		case <-w.updates:
			w.policy.resetEvidence()
			retryAfter = time.Time{}
		case <-ticker.C:
		}
	}
}

// formatPolicy only consumes available bandwidth. It makes no CPU or encoder
// quality inference. Operators choose the ladder for their source and content.
type formatPolicy struct {
	cfg            config.SourceFormatConfig
	timings        config.SourceFormatTimings
	lastObserved   media.SourceFormat
	lastTransition time.Time
	candidate      string
	candidateSince time.Time
}

func newFormatPolicy(cfg config.SourceFormatConfig) (formatPolicy, error) {
	timings, err := cfg.Timings()
	if err != nil {
		return formatPolicy{}, err
	}
	cfg.Profiles = append([]config.SourceFormatProfile(nil), cfg.Profiles...)
	for index := range cfg.Profiles {
		format, err := cfg.Profiles[index].Format.Normalize()
		if err != nil {
			return formatPolicy{}, err
		}
		cfg.Profiles[index].Format = format
	}
	if _, ok := cfg.Profile(cfg.Default); !ok {
		return formatPolicy{}, errors.New("source format default profile is missing")
	}
	return formatPolicy{cfg: cfg, timings: timings}, nil
}

func (p *formatPolicy) resetEvidence() {
	p.candidate = ""
	p.candidateSince = time.Time{}
}

func (p *formatPolicy) confirm(now time.Time, observed media.SourceFormat) {
	p.lastObserved = observed
	p.lastTransition = now
	p.resetEvidence()
}

func (p *formatPolicy) choose(now time.Time, bps int, observed media.SourceFormat, selected string, manual bool) (config.SourceFormatProfile, bool) {
	if observed != p.lastObserved {
		// Late native observations after a timeout/cancellation also establish
		// a dwell period. The first known format is just the startup baseline.
		if p.lastObserved.Width != 0 {
			p.confirm(now, observed)
		}
		p.lastObserved = observed
	}
	if manual || !p.cfg.Adaptive.Enabled {
		p.resetEvidence()
		if !manual {
			selected = p.cfg.Default
		}
		target, ok := p.cfg.Profile(selected)
		return target, ok && target.Format != observed
	}
	current := -1
	for index, profile := range p.cfg.Profiles {
		if profile.Format == observed {
			current = index
			break
		}
	}
	if current < 0 {
		target, _ := p.cfg.Profile(p.cfg.Default)
		return target, true
	}
	if bps <= 0 {
		p.resetEvidence()
		return config.SourceFormatProfile{}, false
	}
	next := current
	if bps < p.cfg.Profiles[current].MinBitrateKbps*1000 {
		next = 0
		for index := 1; index < current; index++ {
			if bps >= p.cfg.Profiles[index].MinBitrateKbps*1000 {
				next = index
			}
		}
	} else if current+1 < len(p.cfg.Profiles) && int64(bps)*100 >= int64(p.cfg.Profiles[current+1].MinBitrateKbps)*1000*int64(100+p.timings.Headroom) {
		// Upgrade only one step after sustained headroom, even after a spike.
		next++
	}
	if next == current {
		p.resetEvidence()
		return config.SourceFormatProfile{}, false
	}
	target := p.cfg.Profiles[next]
	if target.ID != p.candidate {
		p.candidate, p.candidateSince = target.ID, now
	}
	hold := p.timings.DownHold
	if next > current {
		hold = p.timings.UpHold
	}
	ready := now.Sub(p.candidateSince) >= hold && (p.lastTransition.IsZero() || now.Sub(p.lastTransition) >= p.timings.MinDwell)
	return target, ready
}
