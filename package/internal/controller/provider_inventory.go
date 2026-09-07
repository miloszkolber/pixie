package controller

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

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
}

// Concurrent settings and model selectors share the same in-flight inventory,
// including explicit-configuration checks. Completed results are never cached;
// Pi remains authoritative on every subsequent request.
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
	if flight == nil {
		bounded, cancel := context.WithTimeout(context.WithValue(context.Background(), connectionGenerationKey{}, generation), 30*time.Second)
		flight = &providerInventoryFlight{done: make(chan struct{}), cancel: cancel}
		a.providerFlights[key] = flight
		go func() {
			flight.providers, flight.err = a.readProviders(bounded, ids)
			if flight.err == nil {
				flight.err = bounded.Err()
			}
			a.providerMu.Lock()
			if a.providerFlights[key] == flight {
				delete(a.providerFlights, key)
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
		if flight.consumers == 0 {
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
	a.providerMu.Unlock()
}
