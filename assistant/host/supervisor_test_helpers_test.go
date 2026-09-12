package host

import "time"

// Tests must not touch the supervisor's internal maps directly: production
// mutates them under s.mu from child goroutines. These helpers take the same
// lock so test reads and writes are synchronized with production.

func (s *nativeSupervisor) testChild(id string) *nativeChild {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.children[id]
}

func (s *nativeSupervisor) testSession(id string) (nativeSessionRef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.sessions[id]
	return ref, ok
}

func (s *nativeSupervisor) testSetChild(id string, child *nativeChild) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.children[id] = child
}

func (s *nativeSupervisor) testSetSession(id string, ref nativeSessionRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = ref
}

func (s *nativeSupervisor) testSetAcceptTimeout(id string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if child := s.children[id]; child != nil {
		child.acceptTimeout = d
	}
}

// testSubscriberCount reports how many event subscribers are installed. Tests
// use it to wait for connection setup instead of sleeping.
func (s *nativeSupervisor) testSubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribers)
}

// testResidents reports the resident child count and the launching reservation
// count.
func (s *nativeSupervisor) testResidents() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.children), s.launching
}

// testChildrenSnapshot returns a copy of the resident children map so a
// diagnostic can print it without exposing the live map.
func (s *nativeSupervisor) testChildrenSnapshot() map[string]*nativeChild {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := make(map[string]*nativeChild, len(s.children))
	for id, child := range s.children {
		snapshot[id] = child
	}
	return snapshot
}

// testSessionsSnapshot returns a copy of the session registry so a diagnostic
// can print it without exposing the live map.
func (s *nativeSupervisor) testSessionsSnapshot() map[string]nativeSessionRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := make(map[string]nativeSessionRef, len(s.sessions))
	for id, ref := range s.sessions {
		snapshot[id] = ref
	}
	return snapshot
}
