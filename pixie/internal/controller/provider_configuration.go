package controller

import (
	"context"
	"sync"
	"time"
)

// Pi's inventory predicate accepts default commands and optional local URLs.
// Read only field-presence flags to distinguish explicit configuration; never
// copy field values or probe a host command from the application container.
func (a *PiAdmin) resolveDefaultProviderConfiguration(ctx context.Context, providers []piProvider) {
	jobs := make(chan int, len(providers))
	for index := range providers {
		provider := &providers[index]
		provider.Configuration = "reported"
		if provider.ReadinessCheck || !boolDefault(provider.Configured, false) || len(provider.ConfigKeys) == 0 {
			continue
		}
		ambiguous := true
		for _, key := range provider.ConfigKeys {
			if key.OAuthFlow || (key.Required && key.Default == "") {
				ambiguous = false
				break
			}
		}
		if ambiguous {
			jobs <- index
		}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(4, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				provider := &providers[index]
				var response struct {
					Fields []struct {
						IsSet bool `json:"isSet"`
					} `json:"fields"`
				}
				bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
				err := a.call(bounded, "pi.providers.config.read", map[string]any{"providerId": provider.ProviderID}, &response)
				cancel()
				explicit := false
				provider.Configuration = "defaults"
				if err != nil {
					provider.Configuration = "unknown"
				} else {
					for _, field := range response.Fields {
						explicit = explicit || field.IsSet
					}
					if explicit {
						provider.Configuration = "explicit"
					}
				}
				provider.Configured = &explicit
			}
		}()
	}
	workers.Wait()
}
