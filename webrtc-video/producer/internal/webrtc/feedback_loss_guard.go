package webrtc

import (
	"math"
	"sync"
	"time"
)

const (
	feedbackLossGuardActivationRatio = 0.10
	feedbackLossGuardCleanRatio      = 0.02
	feedbackLossGuardMinimumStatuses = 20
	feedbackLossGuardDecreasePeriod  = 200 * time.Millisecond
	feedbackLossGuardRecoveryDelay   = time.Second
	feedbackLossGuardRecoveryPeriod  = 200 * time.Millisecond
	feedbackLossGuardRecoveryFactor  = 1.05
)

type feedbackLossGuard struct {
	mu               sync.Mutex
	now              func() time.Time
	minimumBitrate   int
	active           bool
	targetBitrate    int
	highLossReports  int
	lastDecrease     time.Time
	cleanSince       time.Time
	lastRecovery     time.Time
	lastObservedLoss float64
	reductions       uint64
	recoveries       uint64
}

type feedbackLossGuardSnapshot struct {
	Active           bool
	TargetBitrate    int
	LastObservedLoss float64
	Reductions       uint64
	Recoveries       uint64
}

func newFeedbackLossGuard(minimumBitrate int) *feedbackLossGuard {
	return &feedbackLossGuard{
		now:            time.Now,
		minimumBitrate: max(1, minimumBitrate),
	}
}

func (g *feedbackLossGuard) effectiveBitrate(underlying int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.effectiveBitrateLocked(underlying)
}

func (g *feedbackLossGuard) observe(
	reported int,
	lost int,
	underlying int,
) (int, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	previous := g.effectiveBitrateLocked(underlying)
	if reported < feedbackLossGuardMinimumStatuses || lost < 0 || lost > reported {
		return previous, false
	}
	lossRatio := float64(lost) / float64(reported)
	g.lastObservedLoss = lossRatio
	now := g.now()
	if lossRatio > feedbackLossGuardActivationRatio {
		g.cleanSince = time.Time{}
		g.highLossReports++
		if g.highLossReports < 2 ||
			(!g.lastDecrease.IsZero() && now.Sub(g.lastDecrease) < feedbackLossGuardDecreasePeriod) {
			return previous, false
		}
		candidate := int(math.Floor(float64(previous) * (1 - 0.5*lossRatio)))
		candidate = max(g.minimumBitrate, min(previous, candidate))
		g.active = true
		g.targetBitrate = candidate
		g.lastDecrease = now
		if candidate == previous {
			return candidate, false
		}
		g.reductions++
		return candidate, true
	}
	g.highLossReports = 0
	if !g.active {
		return previous, false
	}
	if underlying < g.targetBitrate {
		g.targetBitrate = max(g.minimumBitrate, underlying)
	}
	if lossRatio >= feedbackLossGuardCleanRatio {
		g.cleanSince = time.Time{}
		return g.effectiveBitrateLocked(underlying), false
	}
	if g.cleanSince.IsZero() {
		g.cleanSince = now
		return g.effectiveBitrateLocked(underlying), false
	}
	if now.Sub(g.cleanSince) < feedbackLossGuardRecoveryDelay ||
		(!g.lastRecovery.IsZero() && now.Sub(g.lastRecovery) < feedbackLossGuardRecoveryPeriod) {
		return g.effectiveBitrateLocked(underlying), false
	}
	candidate := max(g.targetBitrate+1_000, int(math.Ceil(float64(g.targetBitrate)*feedbackLossGuardRecoveryFactor)))
	if candidate >= underlying {
		g.active = false
		g.targetBitrate = 0
		g.lastRecovery = now
		g.recoveries++
		return underlying, underlying != previous
	}
	g.targetBitrate = candidate
	g.lastRecovery = now
	g.recoveries++
	return candidate, candidate != previous
}

func (g *feedbackLossGuard) snapshot() feedbackLossGuardSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return feedbackLossGuardSnapshot{
		Active:           g.active,
		TargetBitrate:    g.targetBitrate,
		LastObservedLoss: g.lastObservedLoss,
		Reductions:       g.reductions,
		Recoveries:       g.recoveries,
	}
}

func (g *feedbackLossGuard) effectiveBitrateLocked(underlying int) int {
	underlying = max(g.minimumBitrate, underlying)
	if !g.active {
		return underlying
	}
	return min(underlying, max(g.minimumBitrate, g.targetBitrate))
}
