package controller

import (
	"sync"
	"sync/atomic"
)

// Session liveness is generic idle-cleanup protection for extension-owned
// work that continues after a prompt goes idle. Providers scope themselves to
// one exact session ID and report whether their work is still active; the
// host lifecycle (explicit stop, archive, delete, shutdown) still takes
// precedence. Liveness never schedules work: it only prevents the inactive
// projection budget from evicting a session an extension still needs.

type sessionLivenessProvider struct {
	name     string
	isActive func() bool
}

var sessionLivenessSerial atomic.Uint64

func (m *SessionManager) ensureLivenessLocked() {
	if m.liveness == nil {
		m.liveness = make(map[string]map[uint64]sessionLivenessProvider)
	}
}

// RegisterSessionLiveness scopes an activity probe to one exact session. The
// returned function unregisters the probe. A failing probe preserves the
// session: losing extension work is worse than delaying eviction.
func (m *SessionManager) RegisterSessionLiveness(sessionID, name string, isActive func() bool) func() {
	if sessionID == "" || name == "" || isActive == nil {
		return func() {}
	}
	id := sessionLivenessSerial.Add(1)
	m.mu.Lock()
	m.ensureLivenessLocked()
	providers := m.liveness[sessionID]
	if providers == nil {
		providers = make(map[uint64]sessionLivenessProvider)
		m.liveness[sessionID] = providers
	}
	providers[id] = sessionLivenessProvider{name: name, isActive: isActive}
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if providers := m.liveness[sessionID]; providers != nil {
				delete(providers, id)
				if len(providers) == 0 {
					delete(m.liveness, sessionID)
				}
			}
			m.mu.Unlock()
		})
	}
}

func (m *SessionManager) hasActiveLivenessLocked(sessionID string) bool {
	for _, provider := range m.liveness[sessionID] {
		active := false
		func() {
			defer func() {
				// A failing probe preserves the session rather than risking
				// extension work the host still owns.
				if recover() != nil {
					active = true
				}
			}()
			active = provider.isActive()
		}()
		if active {
			return true
		}
	}
	return false
}
