package controller

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

// ProviderInventoryTTL bounds how long a successfully resolved provider
// projection is reused. It is short by design: Pi stays authoritative, and a
// configuration mutation invalidates the entry immediately through the
// revision identity guard. Tests override it to exercise expiry without a real
// minute-long wait.
var ProviderInventoryTTL = 60 * time.Second

type providerInventoryKey struct {
	generation, revision uint64
	ids                  string
}

type providerInventoryFlight struct {
	done      chan struct{}
	cancel    context.CancelFunc
	consumers int
	providers []piProvider
	err       error
	// completed marks a finished successful read retained as a short-TTL cache
	// entry. Only the identity key above decides reuse; a mutation bumps the
	// revision so a stale projection cannot be served.
	completed bool
	expiresAt time.Time
}

// Concurrent settings and model selectors share the same in-flight inventory,
// including explicit-configuration checks. A completed successful result is
// reused for ProviderInventoryTTL so usage, availability and rate-limit
// projections do not issue a second policy or a second round trip for every
// settings render. Pi remains authoritative after the TTL or after an
// invalidating mutation.
func (a *PiAdmin) providers(ctx context.Context, ids []string) ([]piProvider, error) {
	generation, err := a.client.Ready(ctx)
	if err != nil {
		return nil, piAdministrationError{cause: err}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids == nil {
		ids = []string{}
	}
	encoded, _ := json.Marshal(ids)
	a.providerMu.Lock()
	key := providerInventoryKey{generation: generation, revision: a.providerRevision, ids: string(encoded)}
	flight := a.providerFlights[key]
	if flight != nil && flight.completed {
		if time.Now().Before(flight.expiresAt) {
			providers, cachedErr := flight.providers, flight.err
			a.providerMu.Unlock()
			return providers, cachedErr
		}
		delete(a.providerFlights, key)
		flight = nil
	}
	if flight == nil {
		bounded, cancel := context.WithTimeout(context.WithValue(context.Background(), connectionGenerationKey{}, generation), 30*time.Second)
		flight = &providerInventoryFlight{done: make(chan struct{}), cancel: cancel}
		a.providerFlights[key] = flight
		go func() {
			providers, runErr := a.readProviders(bounded, ids)
			if runErr == nil {
				runErr = bounded.Err()
			}
			a.providerMu.Lock()
			// Waiters hold this pointer even when an invalidation cleared the
			// map, so always publish the outcome before releasing them.
			flight.providers, flight.err = providers, runErr
			if a.providerFlights[key] == flight {
				// Only the canonical full projection is retained: a targeted
				// readiness or refresh poll must observe fresh native state.
				if runErr == nil && len(ids) == 0 {
					flight.completed = true
					flight.expiresAt = time.Now().Add(ProviderInventoryTTL)
				} else {
					delete(a.providerFlights, key)
				}
			}
			close(flight.done)
			cancel()
			a.providerMu.Unlock()
		}()
	}
	flight.consumers++
	a.providerMu.Unlock()
	defer func() {
		a.providerMu.Lock()
		flight.consumers--
		if flight.consumers == 0 && !flight.completed {
			flight.cancel()
			if a.providerFlights[key] == flight {
				delete(a.providerFlights, key)
			}
		}
		a.providerMu.Unlock()
	}()
	select {
	case <-flight.done:
		// Callers only read this projection; configuration resolution finishes
		// before publication and model/status mapping creates its own values.
		return flight.providers, flight.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *PiAdmin) invalidateProviderInventory() {
	a.providerMu.Lock()
	a.providerRevision++
	// Drop retained projections so a stale provider identity cannot be served
	// under the new revision, and an in-flight read cannot become a cache entry.
	clear(a.providerFlights)
	a.providerMu.Unlock()
}
