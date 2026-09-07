package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type PiAdmin struct {
	client           *PiClient
	settings         *Settings
	publish          func(string, any)
	canonicalSlots   chan struct{}
	canonicalMu      sync.Mutex
	canonicalFlights map[canonicalKey]*canonicalFlight
	providerMu       sync.Mutex
	providerRevision uint64
	providerFlights  map[providerInventoryKey]*providerInventoryFlight
	sessions         *SessionManager
	extensionMu      sync.Mutex
	toolMu           sync.Mutex
	agentMu          sync.Mutex
	logins           *ProviderLogins
}

func NewPiAdmin(client *PiClient, settings *Settings) *PiAdmin {
	admin := &PiAdmin{client: client, settings: settings, canonicalSlots: make(chan struct{}, 4), canonicalFlights: make(map[canonicalKey]*canonicalFlight), providerFlights: make(map[providerInventoryKey]*providerInventoryFlight)}
	admin.logins = NewProviderLogins(admin, nil)
	return admin
}

type piProvider struct {
	ProviderID       string                `json:"providerId"`
	ProviderName     string                `json:"providerName"`
	Name             string                `json:"name"`
	Configured       *bool                 `json:"configured"`
	Available        *bool                 `json:"available"`
	VisibleInSetup   *bool                 `json:"visibleInSetup"`
	Deprecated       bool                  `json:"deprecated"`
	Replacement      string                `json:"replacement"`
	Configuration    string                `json:"-"`
	ReadinessCheck   bool                  `json:"readinessCheck"`
	LastRefreshError string                `json:"lastRefreshError"`
	Refreshing       bool                  `json:"refreshing"`
	ConfigKeys       []piProviderConfigKey `json:"configKeys"`
	Models           []piModel             `json:"models"`
}

type piProviderConfigKey struct {
	Name      string `json:"name"`
	Default   string `json:"default"`
	Secret    bool   `json:"secret"`
	Required  bool   `json:"required"`
	OAuthFlow bool   `json:"oauthFlow"`
	Primary   bool   `json:"primary"`
}

type piModel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ContextLimit    *int     `json:"contextLimit"`
	MaxOutputTokens *int     `json:"maxOutputTokens"`
	Reasoning       *bool    `json:"reasoning"`
	Modalities      []string `json:"modalities"`
}

func (a *PiAdmin) readProviders(ctx context.Context, ids []string) ([]piProvider, error) {
	if ids == nil {
		ids = []string{}
	}
	var response struct {
		Entries []piProvider `json:"entries"`
	}
	if err := a.call(ctx, "pi.providers.list", map[string]any{"providerIds": ids}, &response); err != nil {
		return nil, err
	}
	for _, provider := range response.Entries {
		if provider.ProviderID == "" {
			return nil, fmt.Errorf("Pi provider response is missing providerId")
		}
	}
	a.resolveDefaultProviderConfiguration(ctx, response.Entries)
	return response.Entries, nil
}

func (a *PiAdmin) Models(ctx context.Context) ([]WireModel, error) {
	models, _, err := a.models(ctx)
	return models, err
}

func (a *PiAdmin) models(ctx context.Context) ([]WireModel, bool, error) {
	providers, err := a.providers(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	config, err := a.settings.Get()
	if err != nil {
		return nil, false, err
	}
	hidden := make(map[string]bool, len(config.HiddenModels))
	for _, model := range config.HiddenModels {
		hidden[model.Provider+"\x00"+model.ID] = true
	}
	result := make([]WireModel, 0)
	for _, provider := range providers {
		available := provider.Configured != nil && *provider.Configured && providerRuntimeAvailable(provider)
		for _, model := range provider.Models {
			if model.ID == "" {
				continue
			}
			name := model.Name
			if name == "" {
				name = model.ID
			}
			input := []string(nil)
			if len(model.Modalities) > 0 {
				input = []string{"text"}
				if contains(model.Modalities, "image") {
					input = append(input, "image")
				}
			}
			result = append(result, WireModel{ID: model.ID, Name: name, Provider: provider.ProviderID, ContextWindow: model.ContextLimit, MaxTokens: model.MaxOutputTokens, Reasoning: model.Reasoning, Input: input, Available: available, Hidden: hidden[provider.ProviderID+"\x00"+model.ID]})
		}
	}
	metadataComplete := a.enrichModels(ctx, result)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Name < result[j].Name
	})
	return result, metadataComplete, nil
}

func (a *PiAdmin) enrichModels(parent context.Context, models []WireModel) bool {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	complete := true
	var completeMu sync.Mutex
	for worker := 0; worker < min(4, len(models)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				model := &models[index]
				canonical, completed := a.canonicalModel(ctx, model.Provider, model.ID)
				model.MetadataComplete = &completed
				if !completed {
					completeMu.Lock()
					complete = false
					completeMu.Unlock()
				}
				if canonical == nil || canonical.Provider != model.Provider || canonical.Model != model.ID || canonical.ContextLimit == nil || *canonical.ContextLimit < 0 || canonical.Reasoning == nil || canonical.Currency == "" {
					continue
				}
				if model.ContextWindow == nil {
					model.ContextWindow = canonical.ContextLimit
				}
				if model.MaxTokens == nil {
					model.MaxTokens = canonical.MaxOutputTokens
				}
				if model.Reasoning == nil {
					model.Reasoning = canonical.Reasoning
				}
				if canonical.InputTokenCost != nil && *canonical.InputTokenCost >= 0 && canonical.OutputTokenCost != nil && *canonical.OutputTokenCost >= 0 {
					cost := map[string]any{"input": *canonical.InputTokenCost, "output": *canonical.OutputTokenCost, "currency": canonical.Currency}
					if canonical.CacheReadTokenCost != nil && *canonical.CacheReadTokenCost >= 0 {
						cost["cacheRead"] = *canonical.CacheReadTokenCost
					}
					if canonical.CacheWriteTokenCost != nil && *canonical.CacheWriteTokenCost >= 0 {
						cost["cacheWrite"] = *canonical.CacheWriteTokenCost
					}
					model.Cost = cost
				}
			}
		}()
	}
send:
	for index := range models {
		if !models[index].Available {
			continue
		}
		select {
		case jobs <- index:
		case <-ctx.Done():
			completeMu.Lock()
			complete = false
			completeMu.Unlock()
			break send
		}
	}
	close(jobs)
	workers.Wait()
	return complete
}

func (a *PiAdmin) RefreshModels(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Refreshes must not join an inventory request made before the mutation.
	a.invalidateProviderInventory()
	defer a.invalidateProviderInventory()
	var refresh struct {
		Started []string `json:"started"`
		Skipped []struct {
			ProviderID string `json:"providerId"`
			Reason     string `json:"reason"`
		} `json:"skipped"`
	}
	if err := a.call(ctx, "pi.providers.inventory.refresh", map[string]any{"providerIds": []string{}}, &refresh); err != nil {
		return nil, err
	}
	a.invalidateProviderInventory()
	started := make(map[string]bool, len(refresh.Started))
	for _, providerID := range refresh.Started {
		started[providerID] = true
	}
	for _, skipped := range refresh.Skipped {
		if skipped.Reason == "already_refreshing" || skipped.Reason == "alreadyRefreshing" {
			started[skipped.ProviderID] = true
			refresh.Started = append(refresh.Started, skipped.ProviderID)
		}
	}
	for len(started) > 0 {
		providers, err := a.providers(ctx, refresh.Started)
		if err != nil {
			return nil, err
		}
		seen := make(map[string]bool, len(providers))
		for _, provider := range providers {
			seen[provider.ProviderID] = true
			if started[provider.ProviderID] && !provider.Refreshing {
				delete(started, provider.ProviderID)
			}
		}
		for providerID := range started {
			if !seen[providerID] {
				return nil, fmt.Errorf("Pi model refresh lost provider %s", providerID)
			}
		}
		if len(started) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for Pi model refresh: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	a.invalidateProviderInventory()
	models, metadataComplete, err := a.models(ctx)
	return map[string]any{"models": models, "complete": metadataComplete}, err
}

func (a *PiAdmin) SetModelVisibility(ctx context.Context, provider, id string, hidden bool) ([]WireModel, error) {
	models, err := a.Models(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, model := range models {
		if model.Provider == provider && model.ID == id {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("unknown model: %s/%s", provider, id)
	}
	if _, err := a.settings.SetModelVisibility(provider, id, hidden); err != nil {
		return nil, err
	}
	for index := range models {
		if models[index].Provider == provider && models[index].ID == id {
			models[index].Hidden = hidden
		}
	}
	return models, nil
}

func (a *PiAdmin) SetAllModelVisibility(ctx context.Context, hidden bool) ([]WireModel, error) {
	models, err := a.Models(ctx)
	if err != nil {
		return nil, err
	}
	refs := []ModelReference{}
	for index := range models {
		models[index].Hidden = hidden
		if hidden {
			refs = append(refs, ModelReference{Provider: models[index].Provider, ID: models[index].ID})
		}
	}
	if _, err := a.settings.Update(AppConfigPatch{HiddenModels: &refs}); err != nil {
		return nil, err
	}
	return models, nil
}

func (a *PiAdmin) ProviderStatus(ctx context.Context) (map[string]any, error) {
	providers, err := a.providers(ctx, nil)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		configured := boolDefault(provider.Configured, false)
		available := providerRuntimeAvailable(provider)
		canOAuth, canAPIKey, canConfigure := false, false, false
		for _, key := range provider.ConfigKeys {
			canOAuth = canOAuth || key.OAuthFlow
			canAPIKey = canAPIKey || (!key.OAuthFlow && key.Secret && (key.Primary || key.Required))
			canConfigure = canConfigure || (!key.OAuthFlow && !key.Secret && (key.Primary || key.Required))
		}
		kind := "other"
		if !configured && canOAuth {
			kind = "oauth"
		} else if !configured && canAPIKey {
			kind = "api-key"
		}
		name := provider.ProviderName
		if name == "" {
			name = provider.Name
		}
		if name == "" {
			name = provider.ProviderID
		}
		item := map[string]any{"id": provider.ProviderID, "name": name, "configured": configured, "kind": kind, "canOAuth": canOAuth, "canApiKey": canAPIKey, "canConfigure": canConfigure, "canLogout": configured && (canAPIKey || canOAuth), "readinessCheck": provider.ReadinessCheck, "modelCount": len(provider.Models), "availableModelCount": 0, "deprecated": provider.Deprecated || !boolDefault(provider.VisibleInSetup, true), "replacement": provider.Replacement, "configuration": provider.Configuration}
		if provider.Available != nil {
			item["available"] = *provider.Available
		}
		if configured && available {
			item["availableModelCount"] = len(provider.Models)
		}
		if provider.LastRefreshError != "" {
			item["detail"] = provider.LastRefreshError
		} else if provider.Available != nil && !available {
			item["detail"] = "Provider runtime is unavailable"
		}
		result = append(result, item)
	}
	return map[string]any{"providers": result}, nil
}

func providerRuntimeAvailable(provider piProvider) bool {
	return boolDefault(provider.Available, false)
}

func (a *PiAdmin) ProviderReadiness(ctx context.Context, providerID string) (map[string]any, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || containsNUL(providerID) {
		return nil, fmt.Errorf("invalid provider identifier")
	}
	providers, err := a.providers(ctx, []string{providerID})
	if err != nil {
		return nil, err
	}
	if len(providers) != 1 || providers[0].ProviderID != providerID || !providers[0].ReadinessCheck {
		return nil, fmt.Errorf("provider does not support Pi readiness checks")
	}
	var response struct {
		ProviderID string  `json:"providerId"`
		Ready      *bool   `json:"ready"`
		Error      *string `json:"error"`
		HasIssue   bool    `json:"hasIssue"`
	}
	if err := a.call(ctx, "pi.providers.readiness.check", map[string]any{"providerId": providerID}, &response); err != nil {
		return nil, err
	}
	if response.ProviderID != providerID || response.Ready == nil {
		return nil, fmt.Errorf("Pi readiness response is invalid")
	}
	return map[string]any{"providerId": providerID, "ready": *response.Ready, "hasIssue": response.Error != nil || response.HasIssue}, nil
}

func (a *PiAdmin) LogoutProvider(ctx context.Context, providerID string) error {
	if providerID == "" || containsNUL(providerID) {
		return fmt.Errorf("invalid provider identifier")
	}
	var ignored any
	defer a.invalidateProviderInventory()
	return a.call(ctx, "pi.providers.config.delete", map[string]any{"providerId": providerID}, &ignored)
}

type PiPreferences struct {
	CompactionReserveTokens *float64 `json:"compactionReserveTokens,omitempty"`
	PiThinkingEffort        *string  `json:"piThinkingEffort,omitempty"`
}

func (a *PiAdmin) ReadPreferences(ctx context.Context) (PiPreferences, error) {
	var response struct {
		Values []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"values"`
	}
	if err := a.call(ctx, "pi.preferences.read", map[string]any{"keys": []string{"compactionReserveTokens", "piThinkingEffort"}}, &response); err != nil {
		return PiPreferences{}, err
	}
	if response.Values == nil {
		return PiPreferences{}, fmt.Errorf("Pi preferences response is missing values")
	}
	return normalizePreferences(response.Values)
}

func (a *PiAdmin) SavePreferences(ctx context.Context, preferences PiPreferences) (PiPreferences, error) {
	values, err := preferenceValues(preferences)
	if err != nil {
		return PiPreferences{}, err
	}
	if len(values) > 0 {
		var ignored any
		if err := a.call(ctx, "pi.preferences.save", map[string]any{"values": values}, &ignored); err != nil {
			return PiPreferences{}, err
		}
	}
	return a.ReadPreferences(ctx)
}

func (a *PiAdmin) ResetPreferences(ctx context.Context, keys []string) (PiPreferences, error) {
	if len(keys) < 1 || len(keys) > 2 {
		return PiPreferences{}, fmt.Errorf("malformed Pi preferences request")
	}
	seen := make(map[string]bool)
	configKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if seen[key] || (key != "compactionReserveTokens" && key != "piThinkingEffort") {
			return PiPreferences{}, fmt.Errorf("malformed Pi preferences request")
		}
		seen[key] = true
		configKeys = append(configKeys, key)
	}
	if err := a.call(ctx, "pi.preferences.reset", map[string]any{"keys": configKeys}, nil); err != nil {
		return PiPreferences{}, err
	}
	return a.ReadPreferences(ctx)
}

type PiProviderDefaults struct {
	ProviderID *string `json:"providerId"`
	ModelID    *string `json:"modelId"`
}

func (a *PiAdmin) ReadDefaults(ctx context.Context) (PiProviderDefaults, error) {
	var response PiProviderDefaults
	err := a.call(ctx, "pi.defaults.read", map[string]any{}, &response)
	return response, err
}

func (a *PiAdmin) SaveDefaults(ctx context.Context, providerID string, modelID *string) (PiProviderDefaults, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || containsNUL(providerID) || modelID != nil && (strings.TrimSpace(*modelID) == "" || containsNUL(*modelID)) {
		return PiProviderDefaults{}, fmt.Errorf("malformed Pi defaults request")
	}
	providers, err := a.providers(ctx, nil)
	if err != nil {
		return PiProviderDefaults{}, err
	}
	valid := false
	for _, provider := range providers {
		if provider.ProviderID == providerID && boolDefault(provider.Configured, false) && providerRuntimeAvailable(provider) {
			valid = true
		}
	}
	if !valid {
		return PiProviderDefaults{}, fmt.Errorf("selected default provider is unavailable")
	}
	var response PiProviderDefaults
	if err := a.call(ctx, "pi.defaults.save", map[string]any{"providerId": providerID, "modelId": modelID}, &response); err != nil {
		return PiProviderDefaults{}, err
	}
	if response.ProviderID == nil || *response.ProviderID != providerID || !equalOptionalString(response.ModelID, modelID) {
		return PiProviderDefaults{}, fmt.Errorf("Pi default response does not match the request")
	}
	return response, nil
}

func (a *PiAdmin) ClearDefaults(ctx context.Context) (PiProviderDefaults, error) {
	var response PiProviderDefaults
	if err := a.call(ctx, "pi.defaults.clear", map[string]any{}, &response); err != nil {
		return PiProviderDefaults{}, err
	}
	if response.ProviderID != nil || response.ModelID != nil {
		return PiProviderDefaults{}, fmt.Errorf("Pi default clear response is invalid")
	}
	return response, nil
}

func (a *PiAdmin) call(ctx context.Context, method string, params any, destination any) error {
	raw, err := a.client.CallPi(ctx, method, params)
	if err != nil {
		return piAdministrationError{cause: err}
	}
	if destination == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("decode Pi response for %s: %w", method, err)
	}
	return nil
}

// Administration errors may include upstream configuration or credentials.
// Retain the cause for inspection without exposing it through the browser wire.
type piAdministrationError struct{ cause error }

func (e piAdministrationError) Error() string {
	return "Pi could not complete the administration request"
}
func (e piAdministrationError) Unwrap() error { return e.cause }

func normalizePreferences(values []struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}) (PiPreferences, error) {
	result := PiPreferences{}
	seen := make(map[string]bool)
	for _, entry := range values {
		if seen[entry.Key] {
			return PiPreferences{}, fmt.Errorf("Pi preferences response is invalid")
		}
		seen[entry.Key] = true
		switch entry.Key {
		case "compactionReserveTokens":
			if entry.Value == nil {
				continue
			}
			value, ok := entry.Value.(float64)
			if !ok || value < 1024 || value > 1000000 || value != math.Trunc(value) {
				return PiPreferences{}, fmt.Errorf("Pi auto compact threshold is invalid")
			}
			result.CompactionReserveTokens = &value
		case "piThinkingEffort":
			if entry.Value == nil {
				continue
			}
			value, ok := entry.Value.(string)
			if !ok || !map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}[value] {
				return PiPreferences{}, fmt.Errorf("Pi thinking effort is invalid")
			}
			result.PiThinkingEffort = &value
		default:
			return PiPreferences{}, fmt.Errorf("Pi preferences response is invalid")
		}
	}
	return result, nil
}

func preferenceValues(value PiPreferences) ([]map[string]any, error) {
	result := []map[string]any{}
	if value.CompactionReserveTokens != nil {
		if *value.CompactionReserveTokens < 1024 || *value.CompactionReserveTokens > 1000000 || *value.CompactionReserveTokens != math.Trunc(*value.CompactionReserveTokens) {
			return nil, fmt.Errorf("malformed Pi preferences request")
		}
		result = append(result, map[string]any{"key": "compactionReserveTokens", "value": *value.CompactionReserveTokens})
	}
	if value.PiThinkingEffort != nil {
		if !map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}[*value.PiThinkingEffort] {
			return nil, fmt.Errorf("malformed Pi preferences request")
		}
		result = append(result, map[string]any{"key": "piThinkingEffort", "value": *value.PiThinkingEffort})
	}
	return result, nil
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
