package svc

import (
	"context"
	"sync"
	"time"
)

// MetricsHistory keeps a server-side ring buffer of resource samples so the
// dashboard can draw hour/day-scale trends instead of only the last few
// seconds of WebSocket points (which vanish on reload). The sampler runs
// inside the panel process; nothing is persisted — history starts over on
// restart, which is fine for an at-a-glance trend view.
type MetricsHistory struct {
	sys *System

	mu     sync.Mutex
	points []Metrics // capped ring, oldest first
}

// Sample interval and buffer capacity: one point per 30s over ~26h
// (3168 points ≈ 120KB RAM — cheap, and covers a day with margin).
const (
	metricsSampleEvery = 30 * time.Second
	metricsKeepMax     = 3168
)

func NewMetricsHistory(sys *System) *MetricsHistory {
	return &MetricsHistory{sys: sys}
}

// Record stores a snapshot (the WS stream already produces one every 2s;
// the sampler is the source that fills history independently of viewers).
func (h *MetricsHistory) Record(m Metrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.points = append(h.points, m)
	if len(h.points) > metricsKeepMax {
		// Drop the oldest chunk (not one-by-one) to keep amortized cost low.
		drop := len(h.points) - metricsKeepMax
		if drop > metricsKeepMax/16 {
			drop = metricsKeepMax / 16
		}
		h.points = append(h.points[:0], h.points[drop:]...)
	}
}

// Range returns samples whose timestamp falls inside [since, now], oldest
// first, downsampled to at most `max` points by averaging consecutive
// buckets (so a 24h view renders the same shape as the 1h view).
func (h *MetricsHistory) Range(since time.Time, max int) []Metrics {
	h.mu.Lock()
	defer h.mu.Unlock()
	lo := since.UnixMilli()
	var sel []Metrics
	for _, p := range h.points {
		if p.Time >= lo {
			sel = append(sel, p)
		}
	}
	if len(sel) <= max || max <= 0 {
		return sel
	}
	// Bucket-average downsample.
	out := make([]Metrics, 0, max)
	bucket := float64(len(sel)) / float64(max)
	for i := 0; i < max; i++ {
		a := int(float64(i) * bucket)
		b := int(float64(i+1) * bucket)
		if b <= a {
			b = a + 1
		}
		if b > len(sel) {
			b = len(sel)
		}
		var acc Metrics
		var cpuSum float64
		var mem, swap, rx, tx uint64
		n := float64(b - a)
		for j := a; j < b; j++ {
			p := sel[j]
			cpuSum += p.CPU
			mem += p.MemUsed
			swap += p.SwapUsed
			rx += p.NetRx
			tx += p.NetTx
			if p.MemTotal > acc.MemTotal {
				acc.MemTotal = p.MemTotal
			}
			if p.SwapTotal > acc.SwapTotal {
				acc.SwapTotal = p.SwapTotal
			}
			acc.Time = p.Time
			acc.Load = p.Load
		}
		acc.CPU = cpuSum / n
		acc.MemUsed = uint64(float64(mem) / n)
		acc.SwapUsed = uint64(float64(swap) / n)
		// Network counters are cumulative; keep the bucket's last value so
		// the frontend's delta math still works over the downsampled series.
		acc.NetRx = sel[b-1].NetRx
		acc.NetTx = sel[b-1].NetTx
		out = append(out, acc)
	}
	return out
}

// SamplerLoop records a snapshot every metricsSampleEvery until ctx ends.
func (h *MetricsHistory) SamplerLoop(ctx context.Context) {
	// First sample immediately so a fresh boot has a starting point.
	h.Record(h.sys.MetricsSnapshot())
	t := time.NewTicker(metricsSampleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Record(h.sys.MetricsSnapshot())
		}
	}
}
