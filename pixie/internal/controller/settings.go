package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
)

type ModelReference struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type SignetSettings struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type AppConfig struct {
	Signet       SignetSettings   `json:"signet"`
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
	var signet map[string]json.RawMessage
	_ = json.Unmarshal(raw["signet"], &signet)
	_ = json.Unmarshal(signet["enabled"], &value.Signet.Enabled)
	_ = json.Unmarshal(signet["address"], &value.Signet.Address)
	var port float64
	if json.Unmarshal(signet["port"], &port) == nil && port >= 1 && port <= 65_535 && port == float64(int(port)) {
		value.Signet.Port = int(port)
	}
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

type SignetPatch struct {
	Enabled *bool   `json:"enabled"`
	Address *string `json:"address"`
	Port    *int    `json:"port"`
}

type AppConfigPatch struct {
	Signet       *SignetPatch      `json:"signet"`
	HiddenModels *[]ModelReference `json:"hiddenModels"`
}

type SignetStatus struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"`
	Reachable bool   `json:"reachable"`
}

type Settings struct {
	mu      sync.Mutex
	store   persist.Store
	cached  *AppConfig
	publish func(AppConfig)
	client  *http.Client
}

func NewSettings(store persist.Store, publish func(AppConfig)) *Settings {
	return &Settings{store: store, publish: publish, client: &http.Client{Timeout: 2 * time.Second}}
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
		if patch.Signet != nil {
			if patch.Signet.Enabled != nil {
				next.Signet.Enabled = *patch.Signet.Enabled
			}
			if patch.Signet.Address != nil {
				next.Signet.Address = *patch.Signet.Address
			}
			if patch.Signet.Port != nil {
				next.Signet.Port = *patch.Signet.Port
			}
		}
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
	if err := persist.Write(s.store, "config.json", next, nil); err != nil {
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

func (s *Settings) SignetStatus(ctx context.Context) (SignetStatus, error) {
	config, err := s.Get()
	if err != nil {
		return SignetStatus{}, err
	}
	host := config.Signet.Address
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	endpoint := fmt.Sprintf("http://%s:%d", host, config.Signet.Port)
	status := SignetStatus{Enabled: config.Signet.Enabled, Endpoint: endpoint}
	if !config.Signet.Enabled {
		return status, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health", nil)
	if err != nil {
		return status, nil
	}
	response, err := s.client.Do(request)
	if err == nil {
		status.Reachable = response.StatusCode >= 200 && response.StatusCode < 300
		_ = response.Body.Close()
	}
	return status, nil
}

func defaultConfig() AppConfig {
	return AppConfig{Signet: SignetSettings{Address: "127.0.0.1", Port: 3850}, HiddenModels: []ModelReference{}}
}

func normalizeConfig(value AppConfig) AppConfig {
	address := strings.TrimSpace(value.Signet.Address)
	if address == "" || strings.ContainsAny(address, " \t\r\n/\\\x00") || strings.Contains(address, "://") {
		address = "127.0.0.1"
	}
	port := value.Signet.Port
	if port < 1 || port > 65_535 {
		port = 3850
	}
	return AppConfig{Signet: SignetSettings{Enabled: value.Signet.Enabled, Address: address, Port: port}, HiddenModels: normalizeModelReferences(value.HiddenModels)}
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
