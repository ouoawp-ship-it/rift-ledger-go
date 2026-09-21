package sqlite

import (
	"sync/atomic"
	"time"
)

type dbMetrics struct{ operations, waitNS, workNS, maxWaitNS, maxWorkNS atomic.Int64 }
type Metrics struct {
	Operations int64   `json:"operations"`
	WaitMS     float64 `json:"wait_ms"`
	WorkMS     float64 `json:"work_ms"`
	MaxWaitMS  float64 `json:"max_wait_ms"`
	MaxWorkMS  float64 `json:"max_work_ms"`
}

func maximum(v *atomic.Int64, n int64) {
	for old := v.Load(); n > old; old = v.Load() {
		if v.CompareAndSwap(old, n) {
			break
		}
	}
}

// Only the outer operation is measured; nested Tx queries do not reacquire locks.
func (d *DB) measuredLock() func() {
	start := time.Now()
	d.mu.Lock()
	acquired := time.Now()
	wait := acquired.Sub(start).Nanoseconds()
	d.metrics.waitNS.Add(wait)
	maximum(&d.metrics.maxWaitNS, wait)
	return func() {
		work := time.Since(acquired).Nanoseconds()
		d.metrics.operations.Add(1)
		d.metrics.workNS.Add(work)
		maximum(&d.metrics.maxWorkNS, work)
		d.mu.Unlock()
	}
}
func (d *DB) Metrics() Metrics {
	return Metrics{d.metrics.operations.Load(), float64(d.metrics.waitNS.Load()) / 1e6, float64(d.metrics.workNS.Load()) / 1e6, float64(d.metrics.maxWaitNS.Load()) / 1e6, float64(d.metrics.maxWorkNS.Load()) / 1e6}
}
