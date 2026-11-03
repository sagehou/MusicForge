package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
)

type libraryToolsState struct {
	toolMu      sync.Mutex
	toolWG      sync.WaitGroup
	toolBusy    atomic.Bool
	toolKind    string
	toolCancel  context.CancelFunc
	toolReports map[string]*libraryToolReport
	tagPreviews map[string]tagPreview
}

type libraryToolReport struct {
	loudnessReport
	Issues []toolIssue `json:"issues,omitempty"`
	Groups [][]int64   `json:"groups,omitempty"`
}
type toolIssue struct {
	ID    int64  `json:"id"`
	Error string `json:"error"`
}

func (a *App) saveToolReport(kind string) {
	raw, _ := json.Marshal(a.toolReports[kind])
	if err := a.setMeta("maintenance:"+kind, string(raw)); err != nil {
		a.logger.Warn("maintenance report save failed", "kind", kind, "error", err)
	}
}

func (a *App) getLibraryTool(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("tool")
	if kind != "tags" && kind != "fingerprints" && kind != "storage" && kind != "undo" {
		apiError(w, 404, errors.New("Unknown maintenance operation"))
		return
	}
	report, err := a.libraryToolSnapshot(r.Context(), kind)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, report)
}

func (a *App) libraryToolSnapshot(ctx context.Context, kind string) (libraryToolReport, error) {
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	if a.toolReports == nil {
		a.toolReports = map[string]*libraryToolReport{}
	}
	if a.toolReports[kind] == nil {
		report := &libraryToolReport{loudnessReport: loudnessReport{State: "idle"}}
		raw, err := a.metaContext(ctx, "maintenance:"+kind)
		if err == nil {
			err = json.Unmarshal([]byte(raw), report)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return libraryToolReport{}, err
		}
		if report.State == "running" {
			report.State, report.Current, report.WaitingReason = "stopped", "", ""
		}
		a.toolReports[kind] = report
	}
	report := *a.toolReports[kind]
	report.Issues = append([]toolIssue(nil), report.Issues...)
	report.Groups = append([][]int64(nil), report.Groups...)
	return report, nil
}

func (a *App) stopLibraryTool(w http.ResponseWriter, r *http.Request) {
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	if a.toolKind == r.PathValue("tool") && a.toolCancel != nil {
		a.toolCancel()
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) runLibraryTool(kind string, total int, run func(context.Context)) error {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	a.repairMu.Lock()
	repairing := a.repairRunning
	a.repairMu.Unlock()
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	if repairing || a.loudnessBusy.Load() || a.toolBusy.Load() || a.ioContext.Err() != nil {
		return errors.New("Another output maintenance operation is running")
	}
	if a.toolReports == nil {
		a.toolReports = map[string]*libraryToolReport{}
	}
	a.toolReports[kind] = &libraryToolReport{loudnessReport: loudnessReport{State: "running", Total: total}}
	ctx, cancel := context.WithCancel(a.ioContext)
	a.toolKind, a.toolCancel = kind, cancel
	a.toolBusy.Store(true)
	a.saveToolReport(kind)
	a.toolWG.Add(1)
	go func() {
		defer a.toolWG.Done()
		defer cancel()
		run(ctx)
		a.toolMu.Lock()
		defer a.toolMu.Unlock()
		report := a.toolReports[kind]
		report.State, report.Current = "complete", ""
		if ctx.Err() != nil {
			report.State = "stopped"
		}
		a.saveToolReport(kind)
		a.toolBusy.Store(false)
		a.toolKind, a.toolCancel = "", nil
	}()
	return nil
}

func (a *App) toolProgress(kind string, source Source, updated bool, err error) {
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	report := a.toolReports[kind]
	report.Processed++
	if err != nil {
		report.Failed++
		if len(report.Issues) < 100 {
			report.Issues = append(report.Issues, toolIssue{source.ID, err.Error()})
		}
	} else if updated {
		report.Updated++
	} else {
		report.Skipped++
	}
	a.saveToolReport(kind)
}

func (a *App) toolCurrent(kind, path string) {
	a.toolMu.Lock()
	a.toolReports[kind].Current = path
	a.toolMu.Unlock()
}
