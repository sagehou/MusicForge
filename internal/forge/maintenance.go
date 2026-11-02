package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata"
)

const maintenanceOptionsKey = "maintenance:options"

// Additive metadata keeps the published Settings JSON and job formats intact.
type maintenanceOptions struct {
	Version          int    `json:"version"`
	ScheduleEnabled  bool   `json:"schedule_enabled"`
	Start            string `json:"start"`
	End              string `json:"end"`
	Timezone         string `json:"timezone"`
	DayConcurrency   int    `json:"day_concurrency"`
	PrioritizeNew    bool   `json:"prioritize_new"`
	NotifyURL        string `json:"notify_url"`
	NotifyMode       string `json:"notify_mode"`
	NotifyToken      string `json:"notify_token,omitempty"`
	NotifyCompletion bool   `json:"notify_completion"`
}

func defaultMaintenanceOptions() maintenanceOptions {
	return maintenanceOptions{Version: 1, Start: "22:00", End: "07:00", Timezone: "UTC", NotifyMode: "webhook"}
}

func (o maintenanceOptions) validate() error {
	if o.Version != 1 || o.DayConcurrency < 0 || o.DayConcurrency > 16 {
		return errors.New("Invalid maintenance options version or daytime concurrency")
	}
	for _, value := range []string{o.Start, o.End} {
		if _, err := time.Parse("15:04", value); err != nil || len(value) != 5 {
			return errors.New("Schedule times must use HH:MM")
		}
	}
	if o.ScheduleEnabled && o.Start == o.End {
		return errors.New("Schedule start and end must differ")
	}
	if _, err := time.LoadLocation(o.Timezone); err != nil {
		return errors.New("Use a valid IANA time zone, such as Asia/Shanghai")
	}
	if o.NotifyMode != "webhook" && o.NotifyMode != "ntfy" {
		return errors.New("Notification mode must be webhook or ntfy")
	}
	if len(o.NotifyURL) > 2048 || len(o.NotifyToken) > 4096 || strings.ContainsAny(o.NotifyToken, "\r\n\x00") {
		return errors.New("Invalid notification URL or token")
	}
	if o.NotifyURL != "" {
		u, err := url.Parse(o.NotifyURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("Notification URL must be HTTP(S) without credentials, query or fragment")
		}
	}
	return nil
}

func (a *App) maintenanceOptions(ctx context.Context) (maintenanceOptions, error) {
	options := defaultMaintenanceOptions()
	raw, err := a.metaContext(ctx, maintenanceOptionsKey)
	if errors.Is(err, sql.ErrNoRows) {
		return options, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &options)
	}
	if err == nil {
		err = options.validate()
	}
	return options, err
}

func (o maintenanceOptions) concurrency(configured int, now time.Time) int {
	if !o.ScheduleEnabled {
		return configured
	}
	zone, err := time.LoadLocation(o.Timezone)
	if err != nil {
		return 0
	}
	clock := now.In(zone).Format("15:04")
	inside := clock >= o.Start && clock < o.End
	if o.Start > o.End {
		inside = clock >= o.Start || clock < o.End
	}
	if inside {
		return configured
	}
	return min(configured, o.DayConcurrency)
}

func (a *App) conversionCapacity(ctx context.Context, configured int) (int, error) {
	options, err := a.maintenanceOptions(ctx)
	if err != nil {
		return 0, err
	}
	return options.concurrency(configured, time.Now()), nil
}

func (a *App) waitOutputMaintenanceCapacity(ctx context.Context) error {
	for ctx.Err()==nil {
		s,err:=a.settingsContext(ctx)
		if err!=nil { return err }
		capacity:=0
		if s.Enabled { capacity,err=a.conversionCapacity(ctx,s.Concurrency) }
		if err!=nil { return err }
		a.jobsMu.Lock(); running,err:=a.runningConversions(); a.jobsMu.Unlock()
		if err!=nil { return err }
		if running<capacity { return nil }
		if !pause(ctx,500*time.Millisecond) { return ctx.Err() }
	}
	return ctx.Err()
}

func (a *App) getMaintenance(w http.ResponseWriter, r *http.Request) {
	options, err := a.maintenanceOptions(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	configured := options.NotifyToken != ""
	options.NotifyToken = ""
	respond(w, 200, map[string]any{"options": options, "token_configured": configured,
		"concurrency": options.concurrency(s.Concurrency, time.Now()), "configured_concurrency": s.Concurrency})
}

func buildReason(source Source, encoding Encoding) string {
	if !source.Present || source.Hash == "" {
		return ""
	}
	switch {
	case source.PossiblyDamaged:
		return "possibly_damaged"
	case !source.OutputPresent:
		return "missing"
	case source.BuiltHash != source.Hash:
		return "changed"
	case source.BuiltProfile != encoding.Fingerprint() || source.Output != outputRel(source.Rel, encoding.Codec):
		return "needs_rebuild"
	default:
		return ""
	}
}

type buildPlan struct {
	Encoding       Encoding       `json:"encoding"`
	Tracks         int            `json:"tracks"`
	Skipped        int            `json:"skipped"`
	Unverified     int            `json:"unverified"`
	InputBytes     int64          `json:"input_bytes"`
	OutputBytes    int64          `json:"estimated_output_bytes"`
	CurrentBytes   int64          `json:"current_output_bytes"`
	LargestSource  int64          `json:"largest_source_bytes"`
	StagingLimit   int64          `json:"staging_limit_bytes"`
	UnknownTime    int            `json:"unknown_duration"`
	Reasons        map[string]int `json:"reasons"`
}

func estimateBuild(list []Source, encoding Encoding, limit int64) buildPlan {
	plan := buildPlan{Encoding: encoding, StagingLimit: limit, Reasons: map[string]int{}}
	bitrate := encoding.Bitrate
	if encoding.Codec == "mp3" && encoding.Mode == "vbr" {
		bitrate = []int{245, 225, 190, 175, 165, 130, 115, 100, 85, 65}[encoding.Quality]
	}
	for _, source := range list {
		reason := buildReason(source, encoding)
		if reason == "" {
			plan.Skipped++
			if source.Present && source.Hash == "" {
				plan.Unverified++
			}
			continue
		}
		plan.Tracks++
		plan.Reasons[reason]++
		plan.InputBytes += source.Size
		plan.LargestSource = max(plan.LargestSource, source.Size)
		if source.OutputSize != nil {
			plan.CurrentBytes += *source.OutputSize
		}
		if source.Duration > 0 && !math.IsInf(source.Duration, 0) && !math.IsNaN(source.Duration) {
			plan.OutputBytes += int64(source.Duration * float64(bitrate) * 125)
		} else {
			plan.UnknownTime++
		}
	}
	return plan
}

func (a *App) planBuild(w http.ResponseWriter, r *http.Request) {
	var body selection
	if !decode(w, r, &body) {
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	list, err := a.sourcesWithStatusContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	ids := map[int64]bool{}
	for _, id := range body.IDs {
		ids[id] = true
	}
	selected := []Source{}
	for _, source := range list {
		if body.All || ids[source.ID] {
			selected = append(selected, source)
		}
	}
	limit := a.cfg.StagingMaxBytes
	if limit == 0 {
		limit = 4 * 1024 * 1024 * 1024
	}
	respond(w, 200, estimateBuild(selected, s.Encoding, limit))
}
