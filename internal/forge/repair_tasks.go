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
	"strings"
	"time"
)

const repairTaskPrefix = "repair-task:"
const repairMemberPrefix = "repair-member:"

// Grouping is optional metadata. Each member retains the published per-track
// history format; an older version can still recover interrupted attempts.
const repairGroups = `WITH repair_members AS (
 SELECT r.key,r.value,coalesce(m.value,substr(r.key,13)) AS task_id,
 json_extract(r.value,'$.state') AS state,json_extract(r.value,'$.activity.phase') AS phase
 FROM meta r LEFT JOIN meta m ON m.key='repair-member:'||substr(r.key,13)
 WHERE r.key LIKE 'repair-task:%' AND json_valid(r.value)
), repair_counts AS (
 SELECT task_id,count(*) AS total,sum(state='success') AS done,sum(state='failed') AS failed,
 sum(state='running' AND phase='repair_wait') AS pending,
 sum(state='running' AND coalesce(phase,'')<>'repair_wait') AS running,sum(state='stopped') AS stopped,
 min(json_extract(value,'$.created')) AS created,max(json_extract(value,'$.updated')) AS updated,
 avg(json_extract(value,'$.progress')) AS fraction,max(json_extract(value,'$.stopping')) AS stopping
 FROM repair_members GROUP BY task_id
), repair_groups AS (
 SELECT *,CASE WHEN running>0 OR pending>0 AND done+failed+stopped>0 THEN 'running'
 WHEN pending>0 THEN 'pending' WHEN failed>0 THEN 'failed' WHEN stopped>0 THEN 'stopped' ELSE 'success' END AS task_state
 FROM repair_counts
) `

func validRepairTaskID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 32
}

// Select a single page across both kinds of history before loading details.
// The existing /jobs response remains unchanged unless repairs are requested.
func (a *App) unifiedTaskList(ctx context.Context, state string, limit, offset int) ([]Task, int, error) {
	query := taskGroups + "," + strings.TrimPrefix(repairGroups, "WITH ") + `, feed AS (
	 SELECT 'job:'||task_id AS id,root.created,task_state FROM tasks JOIN jobs root ON root.id=task_id
	 UNION ALL SELECT 'repair:'||task_id,created,task_state FROM repair_groups
	) `
	where, args := "", []any{}
	if state != "" && state != "all" {
		where, args = " WHERE task_state=?", []any{state}
	}
	rows, err := a.reads.QueryContext(ctx, query+"SELECT id,count(*) OVER () FROM feed"+where+" ORDER BY created DESC,id DESC LIMIT ? OFFSET ?", append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	keys, ids, total := []string{}, []int64{}, 0
	for rows.Next() {
		var key string
		if err = rows.Scan(&key, &total); err != nil {
			break
		}
		keys = append(keys, key)
		if strings.HasPrefix(key, "job:") {
			id, _ := strconv.ParseInt(strings.TrimPrefix(key, "job:"), 10, 64)
			ids = append(ids, id)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	list := []Task{}
	if len(keys) == 0 {
		err = a.reads.QueryRowContext(ctx, query+"SELECT count(*) FROM feed"+where, args...).Scan(&total)
		return list, total, err
	}
	byKey := map[string]Task{}
	if len(ids) > 0 {
		jobs, _, err := a.taskListSelected(ctx, "all", limit, 0, ids)
		if err != nil {
			return nil, 0, err
		}
		for _, task := range jobs {
			byKey["job:"+strconv.FormatInt(task.ID, 10)] = task
		}
	}
	repairIDs, repairArgs := []string{}, []any{}
	for _, key := range keys {
		if strings.HasPrefix(key, "repair:") {
			repairIDs = append(repairIDs, "?")
			repairArgs = append(repairArgs, strings.TrimPrefix(key, "repair:"))
		}
	}
	if len(repairIDs) > 0 {
		rows, err := a.reads.QueryContext(ctx, repairGroups+`SELECT task_id,task_state,total,done,failed,pending,running,stopped,created,updated,coalesce(fraction,0),coalesce(stopping,0),
		 (SELECT value FROM repair_members m WHERE m.task_id=g.task_id
		 ORDER BY (state='running' AND coalesce(phase,'')<>'repair_wait') DESC,json_extract(value,'$.updated') DESC,key DESC LIMIT 1)
		 FROM repair_groups g WHERE task_id IN (`+strings.Join(repairIDs, ",")+")", repairArgs...)
		if err != nil { return nil, 0, err }
		for rows.Next() {
			var task Task
			var raw string
			var stopping int
			err = rows.Scan(&task.RepairID, &task.State, &task.Counts.Total, &task.Counts.Done, &task.Counts.Failed,
				&task.Counts.Pending, &task.Counts.Running, &task.Counts.Cancelled, &task.Created, &task.Updated, &task.Progress, &stopping, &raw)
			if err != nil { break }
			var repair repairTask
			if err = json.Unmarshal([]byte(raw), &repair); err != nil { break }
			task.Kind, task.Args = "repair", json.RawMessage(`{}`)
			task.CanDelete = task.Counts.Pending+task.Counts.Running == 0
			task.Stopping, task.Profile = stopping != 0, &repair.Profile
			task.Current, task.Scope = []Activity{}, []string{repair.Activity.Path}
			if !task.CanDelete { task.Current = append(task.Current, repair.Activity) }
			byKey["repair:"+task.RepairID] = task
		}
		if err == nil { err = rows.Err() }
		rows.Close()
		if err != nil { return nil, 0, err }
	}
	for _, key := range keys {
		if task, ok := byKey[key]; ok { list = append(list, task) }
	}
	return list, total, nil
}

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
	return a.repairTaskListGroup(ctx, state, limit, offset, "")
}

func (a *App) repairTaskListGroup(ctx context.Context, state string, limit, offset int, group string) ([]repairTask, int, error) {
	where := " WHERE r.key LIKE 'repair-task:%' AND json_valid(r.value)"
	from := " FROM meta r LEFT JOIN meta m ON m.key='repair-member:'||substr(r.key,13)"
	args := []any{}
	if group != "" {
		where += " AND coalesce(m.value,substr(r.key,13))=?"
		args = append(args, group)
	}
	if state != "" && state != "all" {
		where += " AND json_extract(r.value,'$.state')=?"
		args = append(args, state)
	}
	var total int
	if err := a.reads.QueryRowContext(ctx, "SELECT count(*)"+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := a.reads.QueryContext(ctx, "SELECT r.value"+from+where+" ORDER BY json_extract(r.value,'$.created') DESC,r.key DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
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
	group := r.URL.Query().Get("group")
	if group != "" && !validRepairTaskID(group) { apiError(w, 400, errors.New("invalid repair task ID")); return }
	limit := 25
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 { limit = n }
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, total, err := a.repairTaskListGroup(r.Context(), state, limit, max(0, offset), group)
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

func (a *App) changeRepairGroups(ctx context.Context, action string, ids []string, all bool, state string) (int, error) {
	// A strict retry must never grant consent for best-effort decoding.
	if action != "stop" && action != "delete" { return 0, nil }
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	where, args := " WHERE ", []any{}
	if action == "stop" { where += "pending+running>0" } else { where += "pending+running=0" }
	if all {
		if state != "" && state != "all" { where += " AND task_state=?"; args = append(args, state) }
	} else {
		if len(ids) == 0 { return 0, nil }
		marks := make([]string, len(ids))
		for i, id := range ids { marks[i] = "?"; args = append(args, id) }
		where += " AND task_id IN ("+strings.Join(marks, ",")+")"
	}
	rows, err := a.reads.QueryContext(ctx, repairGroups+"SELECT task_id,key,value FROM repair_members WHERE task_id IN (SELECT task_id FROM repair_groups"+where+")", args...)
	if err != nil { return 0, err }
	groups := map[string][]repairTask{}
	for rows.Next() {
		var id, key, raw string
		var task repairTask
		if err = rows.Scan(&id, &key, &raw); err != nil { break }
		if err = json.Unmarshal([]byte(raw), &task); err != nil { break }
		groups[id] = append(groups[id], task)
	}
	if err == nil { err = rows.Err() }
	rows.Close()
	if err != nil { return 0, err }
	changed := 0
	for _, members := range groups {
		if action == "delete" {
			tx, err := a.db.BeginTx(ctx, nil)
			if err != nil { return changed, err }
			for _, task := range members {
				_, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key IN (?,?)", repairTaskPrefix+task.ID, repairMemberPrefix+task.ID)
				if err != nil { break }
			}
			if err != nil { tx.Rollback(); return changed, err }
			if err = tx.Commit(); err != nil { return changed, err }
			changed++
			continue
		}
		stopped := false
		for _, task := range members {
			entry := a.repairs[task.SourceID]
			if task.State != "running" || entry == nil || entry.task == nil || entry.task.ID != task.ID || entry.task.Stopping { continue }
			entry.task.Stopping, entry.task.Updated = true, time.Now().Unix()
			if err := a.saveRepairTask(entry.task); err != nil { return changed, err }
			entry.cancel()
			stopped = true
		}
		if stopped { changed++ }
	}
	return changed, nil
}
