package forge

import (
	"encoding/json"
	"net/http"
	"strings"
)

var maintenanceFailureReasons = []string{"permission", "space", "storage", "rate_limit", "network", "read", "decode", "encode", "validate", "other"}

func maintenanceFailureReason(message string) string {
	text := strings.ToLower(message)
	for _, rule := range []struct {
		reason  string
		markers []string
	}{
		{"permission", []string{"permission denied", "operation not permitted", "read-only file system"}},
		{"space", []string{"no space left", "disk full", "insufficient space", "quota exceeded"}},
		{"storage", []string{"source mount", "output mount", "storage offline", "source root", "output root", "stale file handle", "transport endpoint is not connected"}},
		{"rate_limit", []string{"cooling down", "http 429", "http 503", "rate limit", "too many requests", "temporarily restricted"}},
		{"network", []string{"check connectivity", "connection refused", "connection reset", "no such host", "network is unreachable", "request timed out", "context deadline exceeded"}},
	} {
		for _, marker := range rule.markers {
			if strings.Contains(text, marker) {
				return rule.reason
			}
		}
	}
	if category := failureCategory(message); category != "" {
		return category
	}
	return "other"
}

func validMaintenanceFailureReason(reason string) bool {
	for _, candidate := range maintenanceFailureReasons {
		if candidate == reason {
			return true
		}
	}
	return false
}

func lyricsFailureMessage(status LyricsStatus) string {
	if maintenanceFailureReason(status.Error) != "other" {
		return status.Error
	}
	for _, attempt := range status.Attempts {
		if attempt.State == "failed" && maintenanceFailureReason(attempt.Error) != "other" {
			return attempt.Error
		}
	}
	return status.Error
}

type maintenanceFailureGroup struct {
	Reason        string                     `json:"reason"`
	Jobs          int                        `json:"jobs"`
	RetryableJobs int                        `json:"retryable_jobs"`
	Lyrics        int                        `json:"lyrics"`
	LyricIDs      []int64                    `json:"lyric_ids"`
	Samples       []maintenanceFailureSample `json:"samples"`
}

type maintenanceFailureSample struct {
	Kind  string `json:"kind"`
	ID    int64  `json:"id"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error"`
}

func (g *maintenanceFailureGroup) sample(kind string, id int64, path, message string) {
	if len(g.Samples) < 3 {
		line := strings.SplitN(message, "\n", 2)[0]
		g.Samples = append(g.Samples, maintenanceFailureSample{kind, id, path, string([]rune(line)[:min(500, len([]rune(line)))])})
	}
}

func (a *App) getMaintenanceFailures(w http.ResponseWriter, r *http.Request) {
	groups := map[string]*maintenanceFailureGroup{}
	group := func(message string) *maintenanceFailureGroup {
		reason := maintenanceFailureReason(message)
		if groups[reason] == nil {
			groups[reason] = &maintenanceFailureGroup{Reason: reason, LyricIDs: []int64{}, Samples: []maintenanceFailureSample{}}
		}
		return groups[reason]
	}
	a.jobsMu.Lock()
	busy := map[int64]bool{}
	for id := range a.activeJobs {
		busy[a.taskIDContext(r.Context(), id)] = true
	}
	a.jobsMu.Unlock()
	rows, err := a.reads.QueryContext(r.Context(), `SELECT j.id,j.log,coalesce(CAST(m.value AS INTEGER),j.id),coalesce(c.value,''),
 NOT EXISTS(SELECT 1 FROM jobs active WHERE active.dedup=j.dedup AND active.state IN ('pending','running')) FROM jobs j
 LEFT JOIN meta m ON m.key='task-member:'||j.id
 LEFT JOIN meta c ON c.key='task-control:'||coalesce(CAST(m.value AS INTEGER),j.id)
 WHERE j.state='failed' AND j.log<>'Stopped by administrator' ORDER BY j.id DESC`)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	for rows.Next() {
		var id, root int64
		var message, control string
		var dedupFree bool
		if err = rows.Scan(&id, &message, &root, &control, &dedupFree); err != nil {
			break
		}
		g := group(message)
		g.Jobs++
		if control == "" && !busy[root] && dedupFree {
			g.RetryableJobs++
		}
		g.sample("jobs", id, "", message)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	// Only a failure still bound to the current registered audio is actionable.
	rows, err = a.reads.QueryContext(r.Context(), `SELECT s.id,s.rel,s.output,m.signature,l.value FROM sources s
 JOIN managed m ON m.source_id=s.id AND m.path=s.output AND m.kind='audio'
 JOIN meta l ON l.key='lyrics-status:'||s.id WHERE s.output_present=1 ORDER BY s.id`)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	for rows.Next() {
		var id int64
		var path, output, signature, raw string
		if err = rows.Scan(&id, &path, &output, &signature, &raw); err != nil {
			break
		}
		var status LyricsStatus
		if json.Unmarshal([]byte(raw), &status) != nil || status.State != "failed" || status.Output != output || status.Signature != signature {
			continue
		}
		message := lyricsFailureMessage(status)
		g := group(message)
		g.Lyrics++
		if len(g.LyricIDs) < 10000 {
			g.LyricIDs = append(g.LyricIDs, id)
		}
		g.sample("lyrics", id, path, message)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	result := []maintenanceFailureGroup{}
	for _, reason := range maintenanceFailureReasons {
		if g := groups[reason]; g != nil {
			result = append(result, *g)
		}
	}
	respond(w, 200, result)
}
