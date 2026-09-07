package diagnostics_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestRequestCounterTracksConcurrentWork(t *testing.T) {
	const requests = 32
	var counter diagnostics.RequestCounter
	var ready sync.WaitGroup
	ready.Add(requests)
	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(requests)
	for index := range requests {
		go func() {
			defer done.Done()
			started := counter.Begin()
			ready.Done()
			<-release
			counter.End(started.Add(-time.Duration(index+1)*time.Millisecond), index%2 == 0)
		}()
	}
	ready.Wait()
	inflight := counter.Snapshot()
	if inflight.Total != requests || inflight.Active != requests || inflight.Failures != 0 {
		t.Fatalf("in-flight snapshot: %#v", inflight)
	}
	close(release)
	done.Wait()
	settled := counter.Snapshot()
	if settled.Total != requests || settled.Failures != requests/2 || settled.Active != 0 {
		t.Fatalf("settled snapshot: %#v", settled)
	}
	if settled.AverageMS <= 0 || settled.MaxMS < settled.AverageMS {
		t.Fatalf("duration snapshot: %#v", settled)
	}
}

func TestDiagnosticSnapshotsBoundUntrustedBuildValues(t *testing.T) {
	defaults := diagnostics.NormalizeBuild("", "")
	if defaults.Version != "0.0.0-dev" || defaults.Revision != "unknown" {
		t.Fatalf("default build = %#v", defaults)
	}
	unsafe := diagnostics.NormalizeBuild("release\nforged", strings.Repeat("r", 200))
	if unsafe.Version != "0.0.0-dev" || len([]rune(unsafe.Revision)) != 128 {
		t.Fatalf("bounded build = %#v", unsafe)
	}
	process := diagnostics.Process(time.Now().Add(-2 * time.Second))
	if process.UptimeSeconds < 1 || process.Goroutines < 1 {
		t.Fatalf("process snapshot = %#v", process)
	}
}
