package forge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Optional task annotations leave schema-2 job kinds, arguments and states intact.
// Older releases still respect paused jobs' not_before and stopped jobs' failed state.
const heldUntil int64 = 253402300799

type taskJobKey struct{}

func taskMemberKey(id int64) string { return "task-member:" + strconv.FormatInt(id, 10) }
func taskControlKey(id int64) string { return "task-control:" + strconv.FormatInt(id, 10) }
func taskProgressKey(id int64) string { return "task-progress:" + strconv.FormatInt(id, 10) }

func (a *App) taskID(id int64) int64 {
	value, err := a.meta(taskMemberKey(id))
	if err == nil {
		if root, err := strconv.ParseInt(value, 10, 64); err == nil {
			return root
		}
	}
	return id
}

type Activity struct {
	Phase     string  `json:"phase"`
	Path      string  `json:"path"`
	Artist    string  `json:"artist"`
	Album     string  `json:"album"`
	Title     string  `json:"title"`
	Processed int     `json:"processed"`
	Total     int     `json:"total"`
	Percent   float64 `json:"percent"`
}

func (a *App) reportProgress(id int64, activity Activity, progress float64) {
	if id == 0 {
		return
	}
	raw, err := json.Marshal(activity)
	if err != nil {
		return
	}
	if err = a.setMeta(taskProgressKey(id), string(raw)); err != nil {
		a.logger.Error("job progress persistence failed", "job", id, "error", err)
	}
	_, _ = a.db.Exec("UPDATE jobs SET progress=?,updated=? WHERE id=? AND state='running'", progress, time.Now().Unix(), id)
	a.logger.Info("job progress", "task", a.taskID(id), "job", id, "phase", activity.Phase,
		"path", activity.Path, "artist", activity.Artist, "album", activity.Album, "title", activity.Title,
		"processed", activity.Processed, "total", activity.Total, "percent", activity.Percent)
}

type TaskCounts struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
	Running int `json:"running"`
}

type Task struct {
	Job
	Counts   TaskCounts `json:"counts"`
	Scan     *Activity  `json:"scan,omitempty"`
	Current  []Activity `json:"current"`
	Scope    []string   `json:"scope"`
	Profile  *Encoding  `json:"profile,omitempty"`
	CanDelete bool      `json:"can_delete"`
}

// Aggregate before pagination so a scan with thousands of tracks remains one task.
const taskGroups = `WITH members AS (
 SELECT j.*,coalesce(CAST(m.value AS INTEGER),j.id) AS task_id FROM jobs j
 LEFT JOIN meta m ON m.key='task-member:'||j.id
), groups AS (
 SELECT task_id, count(*) AS member_count,
 sum(kind IN ('convert','move')) AS track_total,
 sum(kind IN ('convert','move') AND state='success') AS track_done,
 sum(kind IN ('convert','move') AND state='failed' AND log<>'Stopped by administrator') AS track_failed,
 sum(kind IN ('convert','move') AND state='pending') AS track_pending,
 sum(kind IN ('convert','move') AND state='running') AS track_running,
 sum(state='pending') AS pending, sum(state='running') AS running,
 sum(state='failed') AS failed, sum(state='pending' AND not_before=253402300799) AS held,
 sum(state='failed' AND log='Stopped by administrator') AS cancelled,
 max(updated) AS changed, max(attempts) AS tries,
 coalesce(avg(CASE WHEN kind IN ('convert','move') THEN CASE WHEN state='success' THEN 1.0 ELSE progress END END),avg(CASE WHEN state='success' THEN 1.0 ELSE progress END)) AS fraction
 FROM members GROUP BY task_id
), tasks AS (
 SELECT g.*,CASE
 WHEN c.value='stopped' AND cancelled>0 AND pending=0 AND running=0 THEN 'stopped'
 WHEN c.value='paused' AND pending>0 AND pending=held AND running=0 THEN 'paused'
 WHEN running>0 THEN 'running'
 WHEN failed>0 AND track_pending=0 AND root.state IN ('success','failed') THEN 'failed'
 WHEN pending>0 AND member_count>1 AND root.state='success' THEN 'running'
 WHEN pending>0 THEN 'pending'
 WHEN failed>0 THEN 'failed' ELSE 'success' END AS task_state
 FROM groups g JOIN jobs root ON root.id=g.task_id
 LEFT JOIN meta c ON c.key='task-control:'||g.task_id
) `

func (a *App) taskList(state string, limit, offset int) ([]Task, int, error) {
	where := ""
	args := []any{}
	if state != "" && state != "all" {
		where = " WHERE task_state=?"
		args = append(args, state)
	}
	var total int
	if err := a.db.QueryRow(taskGroups+"SELECT count(*) FROM tasks"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	columns := strings.Split(jobCols, ",")
	for i := range columns {
		columns[i] = "root." + columns[i]
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := a.db.Query(taskGroups+"SELECT "+strings.Join(columns, ",")+",task_state,track_total,track_done,track_failed,track_pending,track_running,changed,tries,fraction,pending+running FROM tasks JOIN jobs root ON root.id=task_id"+where+" ORDER BY task_id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	list := []Task{}
	for rows.Next() {
		var task Task
		var raw, taskState string
		var active int
		err = rows.Scan(&task.ID, &task.Kind, &task.Key, &raw, &task.State, &task.Attempts, &task.Progress, &task.Log, &task.Created, &task.Updated,
			&taskState, &task.Counts.Total, &task.Counts.Done, &task.Counts.Failed, &task.Counts.Pending, &task.Counts.Running, &task.Updated, &task.Attempts, &task.Progress, &active)
		if err != nil {
			break
		}
		task.Args = json.RawMessage(raw)
		task.State = taskState
		task.CanDelete = active == 0
		task.Current = []Activity{}
		task.Scope = []string{}
		if task.Kind == "scan" {
			var request ScanRequest
			_ = json.Unmarshal(task.Args, &request)
			if request.Dirs != nil {
				task.Scope = request.Dirs
			}
		} else if task.Kind == "convert" || task.Kind == "move" {
			var request BuildRequest
			_ = json.Unmarshal(task.Args, &request)
			task.Profile = &request.Profile
		}
		list = append(list, task)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for i := range list {
		task := &list[i]
		if task.Profile == nil && task.Counts.Total > 0 {
			var raw string
			err := a.db.QueryRow("SELECT j.args FROM jobs j LEFT JOIN meta m ON m.key='task-member:'||j.id WHERE coalesce(CAST(m.value AS INTEGER),j.id)=? AND j.kind IN ('convert','move') ORDER BY j.id LIMIT 1", task.ID).Scan(&raw)
			if err != nil && err != sql.ErrNoRows { return nil, 0, err }
			if err == nil {
				var request BuildRequest
				if err = json.Unmarshal([]byte(raw), &request); err != nil { return nil, 0, err }
				task.Profile = &request.Profile
			}
		}
		if task.Kind == "scan" {
			if raw, err := a.meta(taskProgressKey(task.ID)); err == nil {
				var activity Activity
				if json.Unmarshal([]byte(raw), &activity) == nil {
					task.Scan = &activity
				}
			}
		}
		items, _, err := a.taskItems(task.ID, "running", 16, 0)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range items {
			task.Current = append(task.Current, item.Activity)
		}
		a.jobsMu.Lock()
		for id := range a.activeJobs {
			if a.taskID(id) == task.ID {
				task.CanDelete = false
			}
		}
		a.jobsMu.Unlock()
	}
	return list, total, nil
}

type TaskItem struct {
	Job
	Activity Activity `json:"activity"`
	Output   string   `json:"output"`
	Profile  *Encoding `json:"profile,omitempty"`
}

func (a *App) taskItems(id int64, state string, limit, offset int) ([]TaskItem, int, error) {
	where := " WHERE coalesce(CAST(m.value AS INTEGER),j.id)=?"
	args := []any{id}
	if state != "" && state != "all" {
		where += " AND j.state=?"
		args = append(args, state)
	}
	var total int
	if err := a.db.QueryRow("SELECT count(*) FROM jobs j LEFT JOIN meta m ON m.key='task-member:'||j.id"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	columns := strings.Split(jobCols, ",")
	for i := range columns {
		columns[i] = "j." + columns[i]
	}
	rows, err := a.db.Query("SELECT "+strings.Join(columns, ",")+",coalesce(p.value,''),coalesce(s.rel,''),coalesce(s.artist,''),coalesce(s.album,''),coalesce(s.title,''),coalesce(s.output,'') FROM jobs j LEFT JOIN meta m ON m.key='task-member:'||j.id LEFT JOIN meta p ON p.key='task-progress:'||j.id LEFT JOIN sources s ON s.id=json_extract(j.args,'$.id')"+where+" ORDER BY CASE j.state WHEN 'running' THEN 0 WHEN 'failed' THEN 1 WHEN 'pending' THEN 2 ELSE 3 END,j.id LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []TaskItem{}
	for rows.Next() {
		var item TaskItem
		var raw, activity string
		err = rows.Scan(&item.ID, &item.Kind, &item.Key, &raw, &item.State, &item.Attempts, &item.Progress, &item.Log, &item.Created, &item.Updated,
			&activity, &item.Activity.Path, &item.Activity.Artist, &item.Activity.Album, &item.Activity.Title, &item.Output)
		if err != nil {
			return nil, 0, err
		}
		item.Args = json.RawMessage(raw)
		if activity != "" {
			_ = json.Unmarshal([]byte(activity), &item.Activity)
		}
		if item.Activity.Phase == "" {
			item.Activity.Phase = item.Kind
		}
		if item.Kind == "convert" || item.Kind == "move" {
			var request BuildRequest
			_ = json.Unmarshal(item.Args, &request)
			item.Profile = &request.Profile
		}
		list = append(list, item)
	}
	return list, total, rows.Err()
}

func (a *App) jobItems(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid task ID"))
		return
	}
	state := r.URL.Query().Get("state")
	if state != "" && state != "all" && state != "failed" {
		apiError(w, 400, errors.New("invalid job state filter"))
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 { offset = 0 }
	items, total, err := a.taskItems(a.taskID(id), state, 50, offset)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "total": total})
}

func (a *App) controlJobs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		selection
		Action string `json:"action"`
	}
	if !decode(w, r, &body) { return }
	switch body.Action {
	case "pause", "resume", "stop", "delete", "retry":
	default:
		apiError(w, 400, errors.New("invalid task action"))
		return
	}
	if body.All && body.Action != "retry" || !body.All && len(body.IDs) == 0 || len(body.IDs) > 10000 {
		apiError(w, 400, errors.New("select tasks"))
		return
	}
	n, err := a.changeTasks(body.Action, body.selection)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 202, map[string]int{"changed": n})
}

func (a *App) changeTasks(action string, chosen selection) (int, error) {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	targets := map[int64]bool{}
	for _, id := range chosen.IDs { targets[a.taskID(id)] = true }
	if chosen.All {
		rows, err := a.db.Query("SELECT DISTINCT coalesce(CAST(m.value AS INTEGER),j.id) FROM jobs j LEFT JOIN meta m ON m.key='task-member:'||j.id WHERE j.state='failed'")
		if err != nil { return 0, err }
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil { rows.Close(); return 0, err }
			targets[id] = true
		}
		err = rows.Err(); rows.Close()
		if err != nil { return 0, err }
	}
	tx, err := a.db.Begin()
	if err != nil { return 0, err }
	defer tx.Rollback()
	cancelIDs := []int64{}
	changed := 0
	for root := range targets {
		rows, err := tx.Query("SELECT j.id,j.state,j.not_before FROM jobs j LEFT JOIN meta m ON m.key='task-member:'||j.id WHERE coalesce(CAST(m.value AS INTEGER),j.id)=?", root)
		if err != nil { return 0, err }
		type member struct { id int64; state string; wait int64 }
		members := []member{}
		for rows.Next() {
			var m member
			if err = rows.Scan(&m.id, &m.state, &m.wait); err != nil { rows.Close(); return 0, err }
			members = append(members, m)
		}
		err = rows.Err(); rows.Close()
		if err != nil { return 0, err }
		if len(members) == 0 { continue }
		var control string
		err = tx.QueryRow("SELECT value FROM meta WHERE key=?", taskControlKey(root)).Scan(&control)
		if err != nil && err != sql.ErrNoRows { return 0, err }
		if action == "retry" && control == "stopped" && chosen.All { continue }
		for _, m := range members {
			_, executing := a.activeJobs[m.id]
			if action == "delete" && (m.state == "pending" || m.state == "running" || executing) {
				return 0, errors.New("stop the task and wait for its worker to finish before deleting history")
			}
			if (action == "resume" || action == "retry") && executing {
				return 0, errors.New("the worker is still pausing; try again shortly")
			}
		}
		eligible := false
		for _, m := range members {
			if (action == "pause" || action == "stop") && (m.state == "pending" || m.state == "running") ||
				action == "resume" && m.state == "pending" && m.wait == heldUntil ||
				action == "retry" && m.state == "failed" || action == "delete" {
				eligible = true
			}
		}
		if !eligible { continue }
		if action == "pause" || action == "stop" {
			value := "paused"
			if action == "stop" { value = "stopped" }
			if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", taskControlKey(root), value); err != nil { return 0, err }
		}
		if action == "resume" || action == "retry" || action == "delete" {
			if _, err = tx.Exec("DELETE FROM meta WHERE key=?", taskControlKey(root)); err != nil { return 0, err }
		}
		for _, m := range members {
			switch action {
			case "pause":
				if m.state != "pending" && m.state != "running" { continue }
				_, err = tx.Exec("UPDATE jobs SET state='pending',not_before=?,updated=? WHERE id=?", heldUntil, time.Now().Unix(), m.id)
				cancelIDs = append(cancelIDs, m.id)
			case "stop":
				if m.state != "pending" && m.state != "running" { continue }
				_, err = tx.Exec("UPDATE jobs SET state='failed',progress=0,log='Stopped by administrator',updated=? WHERE id=?", time.Now().Unix(), m.id)
				cancelIDs = append(cancelIDs, m.id)
				_, _ = tx.Exec("DELETE FROM meta WHERE key=?", "scan-followup:"+strconv.FormatInt(m.id, 10))
			case "resume":
				if m.state != "pending" || m.wait != heldUntil { continue }
				_, err = tx.Exec("UPDATE jobs SET not_before=0,log='',updated=? WHERE id=?", time.Now().Unix(), m.id)
			case "retry":
				if m.state != "failed" { continue }
				_, err = tx.Exec("UPDATE jobs SET state='pending',attempts=0,progress=0,not_before=0,log='',updated=? WHERE id=? AND NOT EXISTS(SELECT 1 FROM jobs active WHERE active.dedup=jobs.dedup AND active.state IN ('pending','running'))", time.Now().Unix(), m.id)
			case "delete":
				if _, err = tx.Exec("DELETE FROM meta WHERE key IN (?,?,?)", taskMemberKey(m.id), taskProgressKey(m.id), "scan-followup:"+strconv.FormatInt(m.id, 10)); err != nil { return 0, err }
				_, err = tx.Exec("DELETE FROM jobs WHERE id=?", m.id)
			}
			if err != nil { return 0, err }
		}
		changed++
	}
	if err = tx.Commit(); err != nil { return 0, err }
	for _, id := range cancelIDs {
		if cancel := a.activeJobs[id]; cancel != nil { cancel() }
	}
	a.logger.Info("task control", "action", action, "tasks", chosen.IDs, "changed", changed)
	return changed, nil
}
