package diagnostics

import (
	"runtime"
	"sync/atomic"
	"time"
)

type RequestCounter struct {
	total               atomic.Uint64
	completed           atomic.Uint64
	failures            atomic.Uint64
	active              atomic.Int64
	totalDurationMicros atomic.Uint64
	maxDurationMicros   atomic.Uint64
}

type RequestSnapshot struct {
	Total     uint64  `json:"total"`
	Failures  uint64  `json:"failures"`
	Active    int64   `json:"active"`
	AverageMS float64 `json:"averageMs"`
	MaxMS     float64 `json:"maxMs"`
}

func (c *RequestCounter) Begin() time.Time {
	c.total.Add(1)
	c.active.Add(1)
	return time.Now()
}

func (c *RequestCounter) End(start time.Time, failed bool) {
	duration := time.Since(start)
	if duration < 0 {
		duration = 0
	}
	micros := uint64(duration.Microseconds())
	c.totalDurationMicros.Add(micros)
	for current := c.maxDurationMicros.Load(); micros > current; current = c.maxDurationMicros.Load() {
		if c.maxDurationMicros.CompareAndSwap(current, micros) {
			break
		}
	}
	if failed {
		c.failures.Add(1)
	}
	c.completed.Add(1)
	c.active.Add(-1)
}

func (c *RequestCounter) Snapshot() RequestSnapshot {
	completed := c.completed.Load()
	average := float64(0)
	if completed != 0 {
		average = float64(c.totalDurationMicros.Load()) / float64(completed) / 1_000
	}
	return RequestSnapshot{
		Total:     c.total.Load(),
		Failures:  c.failures.Load(),
		Active:    c.active.Load(),
		AverageMS: average,
		MaxMS:     float64(c.maxDurationMicros.Load()) / 1_000,
	}
}

type ProcessSnapshot struct {
	UptimeSeconds uint64 `json:"uptimeSeconds"`
	Goroutines    int    `json:"goroutines"`
	HeapBytes     uint64 `json:"heapBytes"`
	GCCycles      uint32 `json:"gcCycles"`
}

func Process(started time.Time) ProcessSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	uptime := time.Duration(0)
	if !started.IsZero() {
		uptime = time.Since(started)
		if uptime < 0 {
			uptime = 0
		}
	}
	return ProcessSnapshot{
		UptimeSeconds: uint64(uptime / time.Second),
		Goroutines:    runtime.NumGoroutine(),
		HeapBytes:     memory.HeapAlloc,
		GCCycles:      memory.NumGC,
	}
}
