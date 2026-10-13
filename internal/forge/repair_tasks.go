package forge

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const repairTaskPrefix = "repair-task:"

// Repair history is optional metadata, never an executable schema-2 job. Older
// workers retain these records without accidentally running a strict conversion.
type repairTask struct {
	ID       string   `json:"id"`
	SourceID int64    `json:"source_id"`
	Hash     string   `json:"source_hash"`
	State    string   `json:"state"`
	Activity Activity `json:"activity"`
	Profile  Encoding `json:"profile"`
	Progress float64  `json:"progress"`
	Created  int64    `json:"created"`
	Updated  int64    `json:"updated"`
	Output   string   `json:"output"`
	Log      string   `json:"log"`
	Stopping bool     `json:"stopping"`
}

type repairTaskKey struct{}

func (a *App) saveRepairTask(task *repairTask) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return a.setMeta(repairTaskPrefix+task.ID, string(raw))
}

func (a *App) recoverRepairTasks() error {
	_, err := a.db.Exec(`UPDATE meta SET value=json_set(value,'$.state','stopped','$.stopping',json('false'),'$.updated',?,'$.log',?)
	 WHERE key LIKE 'repair-task:%' AND json_valid(value) AND json_extract(value,'$.state')='running'`,
		time.Now().Unix(), "Repair interrupted by restart; check Library for any recovered output before retrying")
	return err
}

// The repair uses the existing staging/encoding progress events. Context keeps
// it out of the durable executable queue and does not allocate fake job IDs.
func (a *App) reportMediaProgress(ctx context.Context, job int64, activity Activity, progress float64) {
	task, _ := ctx.Value(repairTaskKey{}).(*repairTask)
	if task == nil {
		a.reportProgress(job, activity, progress)
		return
	}
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	if activity.Phase == "convert" {
		progress = .1 + activity.Percent/100*.75
	}
	task.Activity, task.Progress, task.Updated = activity, max(task.Progress, min(.99, progress)), time.Now().Unix()
	if err := a.saveRepairTask(task); err != nil {
		a.logger.Error("repair progress persistence failed", "repair_task", task.ID, "error", err)
	}
	a.logger.Info("repair progress", "repair_task", task.ID, "source_id", task.SourceID, "path", activity.Path,
		"phase", activity.Phase, "percent", activity.Percent, "read_bytes", activity.ReadBytes, "read_total_bytes", activity.ReadTotalBytes)
}

// Called while repairMu is held, after both children and scratch cleanup finish.
func (a *App) finishRepairTask(task *repairTask, source Source, result RepairStatus, err error, cancelled bool) {
	task.Updated, task.Stopping = time.Now().Unix(), false
	switch {
	case err == nil:
		task.State, task.Progress = "success", 1
		task.Output = outputRel(source.Rel, task.Profile.Codec)
		task.Log = result.Diagnostics
	case cancelled:
		task.State = "stopped"
		task.Log = "Repair stopped; check Library for any output committed before cancellation"
	default:
		task.State = "failed"
		task.Log = fmt.Sprintf("Source: %q\nSource SHA-256: %s\nSource bytes: %d\n%s", source.Rel, source.Hash, source.Size, err)
	}
	if err := a.saveRepairTask(task); err != nil {
		a.logger.Error("repair history persistence failed", "repair_task", task.ID, "error", err)
	}
	a.logger.Info("repair task finished", "repair_task", task.ID, "source_id", task.SourceID, "path", source.Rel, "state", task.State)
}

func (a *App) repairTaskList(ctx context.Context, state string, limit, offset int) ([]repairTask, int, error) {
	where := " WHERE key LIKE 'repair-task:%' AND json_valid(value)"
	args := []any{}
	if state != "" && state != "all" {
		where += " AND json_extract(value,'$.state')=?"
		args = append(args, state)
	}
	var total int
	if err := a.reads.QueryRowContext(ctx, "SELECT count(*) FROM meta"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := a.reads.QueryContext(ctx, "SELECT value FROM meta"+where+" ORDER BY json_extract(value,'$.created') DESC,key DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []repairTask{}
	for rows.Next() {
		var raw string
		var task repairTask
		if err = rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal([]byte(raw), &task); err != nil {
			return nil, 0, err
		}
		list = append(list, task)
	}
	return list, total, rows.Err()
}

func (a *App) listRepairTasks(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	switch state {
	case "", "all", "running", "success", "failed", "stopped":
	default:
		apiError(w, 400, errors.New("invalid job state filter"))
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, total, err := a.repairTaskList(r.Context(), state, 25, max(0, offset))
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]any{"tasks": list, "total": total})
}

func (a *App) controlRepairTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 32 {
		apiError(w, 400, errors.New("invalid repair task ID"))
		return
	}
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	raw, err := a.metaContext(r.Context(), repairTaskPrefix+id)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("repair task not found"))
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	var task repairTask
	if err = json.Unmarshal([]byte(raw), &task); err != nil {
		apiError(w, 500, err)
		return
	}
	if r.Method == http.MethodDelete {
		if task.State == "running" {
			apiError(w, 409, errors.New("Stop the repair and wait for it to finish before deleting history"))
			return
		}
		_, err = a.db.ExecContext(r.Context(), "DELETE FROM meta WHERE key=?", repairTaskPrefix+id)
	} else if task.State == "running" {
		entry := a.repairs[task.SourceID]
		if entry == nil || entry.task == nil || entry.task.ID != id {
			apiError(w, 409, errors.New("Repair is finishing; refresh shortly"))
			return
		}
		entry.task.Stopping, entry.task.Updated = true, time.Now().Unix()
		err = a.saveRepairTask(entry.task)
		if err == nil {
			entry.cancel()
		}
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
