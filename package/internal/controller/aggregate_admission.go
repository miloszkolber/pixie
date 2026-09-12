package controller

import "sync"

// AggregateByteAdmission accounts retained ordinary and reserved control
// bytes for one controller process. The same instance is shared by browser
// request admission, replay retention and socket output queues, so a large
// history response cannot evade the process-wide aggregate ceiling by moving
// between subsystems. Control storage is independent and is never consumed by
// ordinary traffic.
type AggregateByteAdmission struct {
	mu            sync.Mutex
	ordinaryLimit int
	controlLimit  int
	ordinaryBytes int
	controlBytes  int
}

// NewAggregateByteAdmission creates a byte budget. Non-positive limits use
// the controller contract defaults.
func NewAggregateByteAdmission(ordinaryLimit, controlLimit int) *AggregateByteAdmission {
	if ordinaryLimit <= 0 {
		ordinaryLimit = BrowserAggregateMaxBytes
	}
	if controlLimit <= 0 {
		controlLimit = BrowserControlReserveBytes
	}
	return &AggregateByteAdmission{ordinaryLimit: ordinaryLimit, controlLimit: controlLimit}
}

// TryAcquireOrdinary reserves ordinary serialized bytes.
func (a *AggregateByteAdmission) TryAcquireOrdinary(size int) bool {
	if size < 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if size > a.ordinaryLimit || a.ordinaryBytes+size > a.ordinaryLimit {
		return false
	}
	a.ordinaryBytes += size
	return true
}

// ReleaseOrdinary releases a prior ordinary reservation.
func (a *AggregateByteAdmission) ReleaseOrdinary(size int) {
	if size <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ordinaryBytes -= size
	if a.ordinaryBytes < 0 {
		a.ordinaryBytes = 0
	}
}

// TryAcquireControl reserves bytes from the independent control lane.
func (a *AggregateByteAdmission) TryAcquireControl(size int) bool {
	if size < 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if size > a.controlLimit || a.controlBytes+size > a.controlLimit {
		return false
	}
	a.controlBytes += size
	return true
}

// ReleaseControl releases a prior control reservation.
func (a *AggregateByteAdmission) ReleaseControl(size int) {
	if size <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.controlBytes -= size
	if a.controlBytes < 0 {
		a.controlBytes = 0
	}
}

// OrdinaryBytes reports currently retained ordinary bytes.
func (a *AggregateByteAdmission) OrdinaryBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ordinaryBytes
}

// ControlBytes reports currently retained control bytes.
func (a *AggregateByteAdmission) ControlBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.controlBytes
}
