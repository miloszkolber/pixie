package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
)

type ModelReference struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type AppConfig struct {
	HiddenModels []ModelReference `json:"hiddenModels"`
}

// Persisted settings accept partial objects and normalize individual fields,
// as the existing controller does; request patches remain separately typed.
func (c *AppConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("config must be an object")
	}
	value := defaultConfig()
	var models []json.RawMessage
	_ = json.Unmarshal(raw["hiddenModels"], &models)
	for _, model := range models {
		var reference ModelReference
		if json.Unmarshal(model, &reference) == nil {
			value.HiddenModels = append(value.HiddenModels, reference)
		}
	}
	*c = normalizeConfig(value)
	return nil
}

type AppConfigPatch struct {
	HiddenModels *[]ModelReference `json:"hiddenModels"`
}

type Settings struct {
	mu      sync.Mutex
	store   persist.Store
	cached  *AppConfig
	publish func(AppConfig)
	client  *http.Client
	faults  persist.PublishFaults
}

func NewSettings(store persist.Store, publish func(AppConfig)) *Settings {
	return &Settings{store: store, publish: publish, client: &http.Client{Timeout: 2 * time.Second}}
}

// SetPublishFaults injects deterministic publication faults for X04 coverage.
// Production code leaves it zero-valued.
func (s *Settings) SetPublishFaults(faults persist.PublishFaults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = faults
}

func (s *Settings) Get() (AppConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked()
}

func (s *Settings) getLocked() (AppConfig, error) {
	if s.cached != nil {
		return cloneConfig(*s.cached), nil
	}
	var stored AppConfig
	ok, err := persist.Read(s.store, "config.json", &stored, nil)
	if err != nil {
		return AppConfig{}, err
	}
	if !ok {
		stored = defaultConfig()
	}
	normalized := normalizeConfig(stored)
	s.cached = &normalized
	return cloneConfig(normalized), nil
}

func (s *Settings) Update(patch AppConfigPatch) (AppConfig, error) {
	return s.mutate(func(next *AppConfig) {
		if patch.HiddenModels != nil {
			next.HiddenModels = append([]ModelReference(nil), (*patch.HiddenModels)...)
		}
	})
}

// SetModelVisibility applies a single change against the latest persisted list.
func (s *Settings) SetModelVisibility(provider, id string, hidden bool) (AppConfig, error) {
	return s.mutate(func(next *AppConfig) {
		refs := make([]ModelReference, 0, len(next.HiddenModels)+1)
		for _, ref := range next.HiddenModels {
			if ref.Provider != provider || ref.ID != id {
				refs = append(refs, ref)
			}
		}
		if hidden {
			refs = append(refs, ModelReference{Provider: provider, ID: id})
		}
		next.HiddenModels = refs
	})
}

func (s *Settings) mutate(update func(*AppConfig)) (AppConfig, error) {
	s.mu.Lock()
	current, err := s.getLocked()
	if err != nil {
		s.mu.Unlock()
		return AppConfig{}, err
	}
	next := current
	update(&next)
	next = normalizeConfig(next)
	outcome, err := persist.WriteWithOutcome(s.store, "config.json", next, nil, s.faults)
	if err != nil {
		// A durability-uncertain publish leaves the new primary visible while
		// the write reports failure. Reconcile the validated primary so the
		// cache cannot diverge from disk, then still surface the uncertainty.
		if outcome.MustReconcileLedger() {
			if reconciled, reconcileErr := persist.ReconcilePrimary[AppConfig](s.store, "config.json", nil); reconcileErr == nil {
				normalized := normalizeConfig(reconciled)
				s.cached = &normalized
			}
		}
		s.mu.Unlock()
		return AppConfig{}, err
	}
	s.cached = &next
	publish := s.publish
	result := cloneConfig(next)
	s.mu.Unlock()
	if publish != nil {
		publish(result)
	}
	return result, nil
}

func defaultConfig() AppConfig {
	return AppConfig{HiddenModels: []ModelReference{}}
}

func normalizeConfig(value AppConfig) AppConfig {
	return AppConfig{HiddenModels: normalizeModelReferences(value.HiddenModels)}
}

func normalizeModelReferences(values []ModelReference) []ModelReference {
	result := make([]ModelReference, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		if value.Provider == "" || value.ID == "" || containsNUL(value.Provider) || containsNUL(value.ID) {
			continue
		}
		key := value.Provider + "\x00" + value.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func cloneConfig(value AppConfig) AppConfig {
	value.HiddenModels = append([]ModelReference{}, value.HiddenModels...)
	return value
}
