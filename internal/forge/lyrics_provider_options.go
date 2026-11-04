package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
)

const lyricsProvidersKey = "lyrics:providers"
const communityLyricsProvidersKey = "lyrics:community-providers"

func communityLyricsProvider(provider string) bool {
	return provider == "amll" || provider == "unison"
}

type lyricsProviderPolicy struct {
	Version   int      `json:"version"`
	Providers []string `json:"providers"`
}

func validLyricsProviderOrder(providers []string) bool {
	if providers == nil || len(providers) > len(lyricsProviders) {
		return false
	}
	seen := map[string]bool{}
	for _, provider := range providers {
		if !slices.Contains(lyricsProviders, provider) || seen[provider] {
			return false
		}
		seen[provider] = true
	}
	return true
}

// Keep this separate from legacy options, whose older writers replace the JSON.
func (a *App) lyricsProviderOrder(ctx context.Context) ([]string, error) {
	raw, err := a.metaContext(ctx, lyricsProvidersKey)
	providers := append([]string{}, defaultLyricsProviders...)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return nil, err
	} else {
		var policy lyricsProviderPolicy
		if json.Unmarshal([]byte(raw), &policy) != nil || policy.Version != 1 || !validLyricsProviderOrder(policy.Providers) {
			return nil, errors.New("Invalid lyrics provider settings")
		}
		if slices.ContainsFunc(policy.Providers, communityLyricsProvider) {
			return nil, errors.New("Invalid lyrics provider settings")
		}
		providers = policy.Providers
	}
	raw, err = a.metaContext(ctx, communityLyricsProvidersKey)
	if errors.Is(err, sql.ErrNoRows) {
		return providers, nil
	}
	if err != nil {
		return nil, err
	}
	// Keep new names out of the published seven-source policy. Older writers
	// can edit its order without erasing community choices or re-enabling sources.
	var policy lyricsProviderPolicy
	if json.Unmarshal([]byte(raw), &policy) != nil || policy.Version != 1 || !validLyricsProviderOrder(policy.Providers) {
		return nil, errors.New("Invalid lyrics provider settings")
	}
	order, index := []string{}, 0
	for _, provider := range policy.Providers {
		if communityLyricsProvider(provider) {
			order = append(order, provider)
		} else if index < len(providers) {
			order = append(order, providers[index])
			index++
		}
	}
	return append(order, providers[index:]...), nil
}
