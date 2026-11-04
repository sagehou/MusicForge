package forge

import (
	"net/http"
	"time"
)

type maintenanceProgress struct {
	Kind string `json:"kind"`
	loudnessReport
	Stopping bool `json:"stopping,omitempty"`
	Remaining int `json:"remaining,omitempty"`
	Resumable bool `json:"resumable,omitempty"`
}

// Snapshots use existing in-memory reports and schema-2 metadata only. Polling
// must never check mounts, open audio or start maintenance work.
func (a *App) getMaintenanceProgress(w http.ResponseWriter, r *http.Request) {
	reports := []maintenanceProgress{}
	a.lyricsMu.Lock()
	if report := a.lyricsReport; report != nil {
		reports = append(reports, maintenanceProgress{Kind: "lyrics", loudnessReport: loudnessReport{
			State: report.State, Total: report.Total, Processed: report.Processed, Updated: report.Found,
			Skipped: report.Skipped, Failed: report.Failed, Current: report.Current, Error: report.Error,
		}, Stopping: report.Stopping, Remaining: report.Remaining, Resumable: report.Resumable})
	}
	type cooldown struct {
		Provider string `json:"provider"`
		RetryAt  int64  `json:"retry_at"`
	}
	a.lyricsMu.Unlock()
	untilByProvider := a.lyricsProviderCooldowns()
	cooldowns := []cooldown{}
	for _, provider := range lyricsProviders {
		if until := untilByProvider[provider]; until.After(time.Now().Add(2 * time.Second)) {
			cooldowns = append(cooldowns, cooldown{provider, until.Unix()})
		}
	}
	gain, err := a.loudnessSnapshot(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if gain.State != "idle" {
		reports = append(reports, maintenanceProgress{Kind: "loudness", loudnessReport: gain})
	}
	for _, kind := range []string{"tags", "fingerprints", "storage", "undo"} {
		report, err := a.libraryToolSnapshot(r.Context(), kind)
		if err != nil {
			apiError(w, 500, err)
			return
		}
		if report.State != "idle" {
			reports = append(reports, maintenanceProgress{Kind: kind, loudnessReport: report.loudnessReport})
		}
	}
	respond(w, 200, map[string]any{"reports": reports, "cooldowns": cooldowns})
}
