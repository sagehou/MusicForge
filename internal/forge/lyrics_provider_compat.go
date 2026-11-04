package forge

import "encoding/json"

// Published schema-2 frontends have a closed list of provider labels. Keep
// additional providers outside those legacy fields so a rollback can render
// the library and preserve ready results without knowing the new provider.
func legacyLyricsProvider(provider string) bool {
	switch provider {
	case "", "source", "lrclib", "qq", "kugou", "netease":
		return true
	}
	return false
}

type lyricsStatusJSON LyricsStatus

func (status LyricsStatus) MarshalJSON() ([]byte, error) {
	value := lyricsStatusJSON(status)
	var additionalProvider, communityProvider string
	if communityLyricsProvider(value.Provider) {
		communityProvider, value.Provider = value.Provider, ""
	} else if !legacyLyricsProvider(value.Provider) {
		additionalProvider, value.Provider = value.Provider, ""
	}
	var additionalAttempts, communityAttempts []lyricsAttempt
	value.Attempts = nil
	for _, attempt := range status.Attempts {
		if communityLyricsProvider(attempt.Provider) {
			communityAttempts = append(communityAttempts, attempt)
		} else if legacyLyricsProvider(attempt.Provider) {
			value.Attempts = append(value.Attempts, attempt)
		} else {
			additionalAttempts = append(additionalAttempts, attempt)
		}
	}
	return json.Marshal(struct {
		lyricsStatusJSON
		AdditionalProvider string          `json:"additional_provider,omitempty"`
		AdditionalAttempts []lyricsAttempt `json:"additional_attempts,omitempty"`
		CommunityProvider  string          `json:"community_provider,omitempty"`
		CommunityAttempts  []lyricsAttempt `json:"community_attempts,omitempty"`
	}{value, additionalProvider, additionalAttempts, communityProvider, communityAttempts})
}

func (status *LyricsStatus) UnmarshalJSON(raw []byte) error {
	var value struct {
		lyricsStatusJSON
		AdditionalProvider string          `json:"additional_provider"`
		AdditionalAttempts []lyricsAttempt `json:"additional_attempts"`
		CommunityProvider  string          `json:"community_provider"`
		CommunityAttempts  []lyricsAttempt `json:"community_attempts"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*status = LyricsStatus(value.lyricsStatusJSON)
	if status.Provider == "" {
		status.Provider = value.AdditionalProvider
	}
	if status.Provider == "" {
		status.Provider = value.CommunityProvider
	}
	status.Attempts = append(status.Attempts, value.AdditionalAttempts...)
	status.Attempts = append(status.Attempts, value.CommunityAttempts...)
	return nil
}

type lyricsFileJSON lyricsFile

func (file lyricsFile) MarshalJSON() ([]byte, error) {
	value := lyricsFileJSON(file)
	var additionalProvider, communityProvider string
	if communityLyricsProvider(value.Provider) {
		communityProvider, value.Provider = value.Provider, ""
	} else if !legacyLyricsProvider(value.Provider) {
		additionalProvider, value.Provider = value.Provider, ""
	}
	return json.Marshal(struct {
		lyricsFileJSON
		AdditionalProvider string `json:"additional_provider,omitempty"`
		CommunityProvider  string `json:"community_provider,omitempty"`
	}{value, additionalProvider, communityProvider})
}

func (file *lyricsFile) UnmarshalJSON(raw []byte) error {
	var value struct {
		lyricsFileJSON
		AdditionalProvider string `json:"additional_provider"`
		CommunityProvider  string `json:"community_provider"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*file = lyricsFile(value.lyricsFileJSON)
	if file.Provider == "" {
		file.Provider = value.AdditionalProvider
	}
	if file.Provider == "" {
		file.Provider = value.CommunityProvider
	}
	return nil
}
