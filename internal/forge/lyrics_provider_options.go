package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
)

const lyricsProvidersKey = "lyrics:providers"

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
	if errors.Is(err, sql.ErrNoRows) {
		return append([]string{}, lyricsProviders...), nil
	}
	if err != nil {
		return nil, err
	}
	var policy lyricsProviderPolicy
	if json.Unmarshal([]byte(raw), &policy) != nil || policy.Version != 1 || !validLyricsProviderOrder(policy.Providers) {
		return nil, errors.New("Invalid lyrics provider settings")
	}
	return policy.Providers, nil
}
