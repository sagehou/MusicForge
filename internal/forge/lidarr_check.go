package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Optional connection data is separate from the published Settings document:
// older schema-2 Settings writers preserve this key without knowing its format.
const lidarrConnectionKey = "lidarr:connection"

type lidarrConnection struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

type lidarrDifference struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	SourceID int64  `json:"source_id,omitempty"`
	Error    string `json:"error,omitempty"`
	ScanDir  string `json:"scan_dir,omitempty"`
}

type lidarrReport struct {
	State          string             `json:"state"`
	Error          string             `json:"error,omitempty"`
	Started        int64              `json:"started"`
	Finished       int64              `json:"finished"`
	Processed      int                `json:"processed"`
	Total          int                `json:"total"`
	Source         string             `json:"source"`
	Prefix         string             `json:"prefix"`
	LidarrCount    int                `json:"lidarr_count"`
	SourceCount    int                `json:"source_count"`
	Matched        int                `json:"matched"`
	OnlyLidarr     int                `json:"only_lidarr"`
	OnlyMusicForge int                `json:"only_musicforge"`
	Unmapped       int                `json:"unmapped"`
	OutsideScope   int                `json:"outside_scope"`
	Differences    []lidarrDifference `json:"differences"`
}

type lidarrCheckState struct {
	lidarrMu     sync.Mutex
	lidarrWG     sync.WaitGroup
	lidarrReport *lidarrReport
}

func (a *App) lidarrConnection(ctx context.Context) (lidarrConnection, error) {
	var c lidarrConnection
	raw, err := a.metaContext(ctx, lidarrConnectionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}

func (a *App) getLidarrConnection(w http.ResponseWriter, r *http.Request) {
	c, err := a.lidarrConnection(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]any{"url": c.URL, "key_configured": c.APIKey != ""})
}

func (a *App) putLidarrConnection(w http.ResponseWriter, r *http.Request) {
	var body lidarrConnection
	if !decode(w, r, &body) {
		return
	}
	a.lidarrMu.Lock()
	defer a.lidarrMu.Unlock()
	if a.lidarrReport != nil && a.lidarrReport.State == "running" {
		apiError(w, 409, errors.New("Lidarr comparison is running"))
		return
	}
	body, err := a.prepareLidarrConnection(r.Context(), body)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	raw, _ := json.Marshal(body)
	if err = a.setMeta(lidarrConnectionKey, string(raw)); err != nil {
		apiError(w, 500, err)
		return
	}
	a.lidarrReport = nil
	respond(w, 200, map[string]bool{"saved": true})
}

func (a *App) prepareLidarrConnection(ctx context.Context, body lidarrConnection) (lidarrConnection, error) {
	if a.lidarrReport != nil && a.lidarrReport.State == "running" {
		return body, errors.New("Lidarr comparison is running")
	}
	body.URL = strings.TrimRight(strings.TrimSpace(body.URL), "/")
	body.APIKey = strings.TrimSpace(body.APIKey)
	if body.URL != "" {
		u, err := url.Parse(body.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.ContainsAny(body.URL, "?#") || len(body.URL) > 2048 {
			return body, errors.New("Invalid Lidarr URL; use HTTP(S) without credentials, query or fragment")
		}
	}
	if len(body.APIKey) > 512 || strings.ContainsAny(body.APIKey, "\r\n") {
		return body, errors.New("Invalid Lidarr API key")
	}
	old, err := a.lidarrConnection(ctx)
	if err != nil {
		return body, err
	}
	if body.URL == "" {
		body.APIKey = ""
	} else {
		if body.APIKey == "" && body.URL == old.URL {
			body.APIKey = old.APIKey
		}
		if body.APIKey == "" {
			return body, errors.New("Enter the Lidarr API key when configuring or changing the URL")
		}
	}
	return body, nil
}

func (a *App) getLidarrCheck(w http.ResponseWriter, r *http.Request) {
	a.lidarrMu.Lock()
	defer a.lidarrMu.Unlock()
	if a.lidarrReport == nil {
		respond(w, 200, lidarrReport{State: "idle", Differences: []lidarrDifference{}})
		return
	}
	respond(w, 200, a.lidarrReport)
}

func (a *App) startLidarrCheck(w http.ResponseWriter, r *http.Request) {
	a.lidarrMu.Lock()
	defer a.lidarrMu.Unlock()
	if a.lidarrReport != nil && a.lidarrReport.State == "running" {
		respond(w, 202, a.lidarrReport)
		return
	}
	if err := a.ioContext.Err(); err != nil {
		apiError(w, 503, err)
		return
	}
	c, err := a.lidarrConnection(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if c.URL == "" || c.APIKey == "" {
		apiError(w, 409, errors.New("Configure the Lidarr connection first"))
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	report := &lidarrReport{State: "running", Started: time.Now().Unix(), Source: s.Source, Prefix: s.LidarrPrefix, Differences: []lidarrDifference{}}
	a.lidarrReport = report
	a.lidarrWG.Add(1)
	go func() {
		defer a.lidarrWG.Done()
		ctx, cancel := context.WithTimeout(a.ioContext, 10*time.Minute)
		defer cancel()
		result, err := a.compareLidarr(ctx, c, s, func(done, total int) {
			a.lidarrMu.Lock()
			report.Processed, report.Total = done, total
			a.lidarrMu.Unlock()
		})
		a.lidarrMu.Lock()
		defer a.lidarrMu.Unlock()
		if err != nil {
			report.State, report.Error = "failed", err.Error()
		} else {
			result.State, result.Started = "complete", report.Started
			result.Processed, result.Total = report.Processed, report.Total
			*report = result
		}
		report.Finished = time.Now().Unix()
	}()
	respond(w, 202, report)
}

type lidarrFile struct {
	Path string `json:"path"`
}

func lidarrGET(ctx context.Context, client *http.Client, c lidarrConnection, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/v1/"+endpoint, nil)
	if err != nil {
		return errors.New("Invalid Lidarr URL")
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		// Do not expose remote response bodies, URL credentials or API keys.
		if ctx.Err() != nil {
			return errors.New("Lidarr comparison timed out or was cancelled")
		}
		return errors.New("Lidarr request failed; check the URL, connection and TLS certificate")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Lidarr returned HTTP %d; check the API key and URL", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32*1024*1024+1))
	text := strings.TrimSpace(string(raw))
	if err != nil || len(raw) > 32*1024*1024 || len(text) == 0 || text[0] != '[' || json.Unmarshal(raw, target) != nil {
		return errors.New("Lidarr returned an invalid or oversized file list; comparison incomplete")
	}
	return nil
}

func (a *App) compareLidarr(ctx context.Context, c lidarrConnection, s Settings, progress func(int, int)) (lidarrReport, error) {
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var artists []struct {
		ID int64 `json:"id"`
	}
	var files, unmapped []lidarrFile
	if err := lidarrGET(ctx, client, c, "artist", &artists); err != nil {
		return lidarrReport{}, err
	}
	progress(0, len(artists)+1)
	for i, artist := range artists {
		if artist.ID <= 0 {
			return lidarrReport{}, errors.New("Lidarr returned an invalid artist ID; comparison incomplete")
		}
		var tracks []lidarrFile
		if err := lidarrGET(ctx, client, c, "trackfile?artistId="+strconv.FormatInt(artist.ID, 10), &tracks); err != nil {
			return lidarrReport{}, err
		}
		files = append(files, tracks...)
		if len(files) > 200000 {
			return lidarrReport{}, errors.New("Lidarr comparison supports up to 200000 file records")
		}
		progress(i+1, len(artists)+1)
	}
	if err := lidarrGET(ctx, client, c, "trackfile?unmapped=true", &unmapped); err != nil {
		return lidarrReport{}, err
	}
	progress(len(artists)+1, len(artists)+1)
	current, err := a.settingsContext(ctx)
	if err != nil {
		return lidarrReport{}, err
	}
	if current.Source != s.Source || current.LidarrPrefix != s.LidarrPrefix {
		return lidarrReport{}, errors.New("Library paths changed; run the comparison again")
	}
	sources, err := a.sourcesWithStatusContext(ctx)
	if err != nil {
		return lidarrReport{}, err
	}
	return compareLidarrFiles(s, sources, files, unmapped)
}

func compareLidarrFiles(s Settings, sources []Source, files, unmapped []lidarrFile) (lidarrReport, error) {
	r := lidarrReport{Source: s.Source, Prefix: s.LidarrPrefix, Differences: []lidarrDifference{}}
	imported, unassigned, outside := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, group := range [][]lidarrFile{files, unmapped} {
		for _, file := range group {
			if file.Path == "" || !filepath.IsAbs(file.Path) {
				return r, errors.New("Lidarr returned a missing or non-absolute file path; comparison incomplete")
			}
			rel, err := mapLidarrPath(s, file.Path)
			if err != nil {
				outside[filepath.Clean(file.Path)] = true
				continue
			}
			if i == 0 {
				imported[rel] = true
			} else {
				unassigned[rel] = true
			}
		}
	}
	r.LidarrCount, r.Unmapped, r.OutsideScope = len(imported), len(unassigned), len(outside)
	for path := range outside {
		r.Differences = append(r.Differences, lidarrDifference{Path: path, Kind: "outside_scope"})
	}
	indexed := make(map[string]Source, len(sources))
	for _, source := range sources {
		indexed[source.Rel] = source
		if !source.Present {
			continue
		}
		r.SourceCount++
		if imported[source.Rel] {
			r.Matched++
		} else {
			r.OnlyMusicForge++
			kind := "not_in_lidarr"
			if unassigned[source.Rel] {
				kind = "unmapped"
			}
			r.Differences = append(r.Differences, lidarrDifference{Path: source.Rel, Kind: kind, SourceID: source.ID})
		}
	}
	for rel := range imported {
		source, exists := indexed[rel]
		diff := lidarrDifference{Path: rel, SourceID: source.ID, Error: source.Error}
		switch {
		case !exists || !source.Present:
			r.OnlyLidarr++
			diff.Kind = "not_indexed"
			if exists {
				diff.Kind = "expired"
			}
			if audioSourcePath(rel) {
				diff.ScanDir = filepath.Dir(rel)
			} else {
				diff.Kind = "unsupported"
			}
		case source.Error != "":
			diff.Kind = "failed"
		case source.PossiblyDamaged:
			diff.Kind = "possibly_damaged"
		case !source.OutputPresent:
			diff.Kind = "no_output"
		default:
			continue
		}
		r.Differences = append(r.Differences, diff)
	}
	for rel := range unassigned {
		if imported[rel] || indexed[rel].Present {
			continue
		}
		diff := lidarrDifference{Path: rel, Kind: "unmapped"}
		if audioSourcePath(rel) {
			diff.ScanDir = filepath.Dir(rel)
		}
		r.Differences = append(r.Differences, diff)
	}
	sort.Slice(r.Differences, func(i, j int) bool { return r.Differences[i].Path < r.Differences[j].Path })
	return r, nil
}
