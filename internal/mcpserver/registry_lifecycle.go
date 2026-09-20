package mcpserver

import (
	"fmt"

	"github.com/miloszkolber/pixie/internal/persist"
)

// LifecycleSnapshot reports independent desired enablement versus actual
// readiness for one module. Desired survives startup failure; Ready reflects
// whether the module can currently accept work.
type LifecycleSnapshot struct {
	Desired bool
	Ready   bool
	Detail  string
}

// DesiredEnabled reports persisted desired enablement without consulting
// readiness. It never starts, stops, or probes the module.
func (r *Registry) DesiredEnabled(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled[id]
}

// ModuleLifecycle snapshots independent desired versus readiness states for
// one module. A failed startup keeps desired true with readiness failed; it
// never erases user intent.
func (r *Registry) ModuleLifecycle(id string) LifecycleSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, ok := r.definitionLocked(id)
	if !ok {
		return LifecycleSnapshot{Detail: fmt.Sprintf("unknown in-process MCP module %q", id)}
	}
	desired := r.enabled[id]
	if !desired {
		return LifecycleSnapshot{Desired: false, Ready: false, Detail: fmt.Sprintf("The %s module is disabled in Pixie MCP servers.", definition.DisplayName)}
	}
	ready, detail := r.healthLocked(id)
	if !ready {
		return LifecycleSnapshot{Desired: true, Ready: false, Detail: detail}
	}
	return LifecycleSnapshot{Desired: true, Ready: true}
}

// validateCompleteState accepts the complete candidate module map, preserving
// unrelated and unknown saved entries without routing or executing them.
// Unknown IDs stay durable but never become runnable modules.
func validateCompleteState(value persistedState) error {
	if value.Modules == nil {
		return fmt.Errorf("modules must be an object")
	}
	return nil
}

// ReadCompleteState rereads the complete persisted module map with the
// preserving validator. Unknown entries survive the read.
func ReadCompleteState(store persist.Store) (persistedState, bool, error) {
	var state persistedState
	found, err := persist.Read(store, storeFile, &state, validateCompleteState)
	return state, found, err
}

// CompleteStateWithDesired folds one desired change into a complete map,
// preserving unrelated and unknown entries.
func CompleteStateWithDesired(current persistedState, id string, enabled bool) persistedState {
	next := persistedState{Modules: make(map[string]persistedModule, len(current.Modules)+1)}
	for key, value := range current.Modules {
		next.Modules[key] = value
	}
	next.Modules[id] = persistedModule{Enabled: enabled}
	return next
}

// WriteCompleteStateWithOutcome publishes the complete candidate map at the
// declared commit point, classifying pre-publication, installed, and
// durability-uncertain outcomes. It never replays an old backup over a
// visible primary.
func WriteCompleteStateWithOutcome(store persist.Store, state persistedState, faults persist.PublishFaults) (persist.PublishOutcome, error) {
	return persist.WriteWithOutcome(store, storeFile, state, validateCompleteState, faults)
}

// ReconcileCompleteState rereads only the validated primary after an
// uncertain outcome. It never falls back to the backup generation.
func ReconcileCompleteState(store persist.Store) (persistedState, error) {
	return persist.ReconcilePrimary(store, storeFile, validateCompleteState)
}

// DecideModuleStart reports whether a privileged worker may start after
// publication. Only an installed outcome allows it. An uncertain outcome must
// reconcile the validated primary first and never start or reauthorize work
// on an unconfirmed commit.
func DecideModuleStart(outcome persist.PublishOutcome) (bool, string) {
	if outcome.MayDispatch() {
		return true, "installed and durable; worker start remains gated by readiness"
	}
	if outcome.Kind == persist.OutcomeDurabilityUncertain {
		return false, "durability uncertain; retain mutation identity and reconcile the validated primary before starting work"
	}
	return false, "known pre-publication failure; prior committed state preserved"
}
