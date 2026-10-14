package forge

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Forget only rebuildable index rows. File deletion remains a worker operation;
// this endpoint never touches either mount or changes persisted job arguments.
func (a *App) forgetSources(w http.ResponseWriter, r *http.Request) {
	var body selection
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) > 10000 {
		apiError(w, 400, errors.New("Select at most 10000 library records at a time"))
		return
	}
	ids := map[int64]bool{}
	for _, id := range body.IDs {
		if id <= 0 {
			apiError(w, 400, errors.New("Library record IDs must be positive"))
			return
		}
		ids[id] = true
	}
	chosen := make([]int64, 0, len(ids))
	for id := range ids {
		chosen = append(chosen, id)
	}
	selected, _ := json.Marshal(chosen)
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	if !a.files.TryLock() {
		apiError(w, 409, errors.New("Library file operation in progress; try removing records again shortly"))
		return
	}
	defer a.files.Unlock()
	active := []string{"-1"}
	for id := range a.activeJobs {
		active = append(active, strconv.FormatInt(id, 10))
	}
	repairing := []int64{}
	a.repairMu.Lock()
	for id, entry := range a.repairs {
		if entry.State == "running" {
			repairing = append(repairing, id)
		}
	}
	a.repairMu.Unlock()
	repairs, _ := json.Marshal(repairing)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	defer tx.Rollback()
	where := "id IN (SELECT value FROM json_each(?))"
	args := []any{string(selected)}
	if body.All {
		where, args = "present=0 AND output=''", nil
	}
	var count int
	if err = tx.QueryRowContext(r.Context(), "SELECT count(*) FROM sources WHERE "+where, args...).Scan(&count); err != nil {
		apiError(w, 500, err)
		return
	}
	query := `WITH busy AS MATERIALIZED (
	 SELECT kind,dedup,args FROM jobs WHERE state IN ('pending','running') OR id IN (` + strings.Join(active, ",") + `)
	) DELETE FROM sources WHERE ` + where + `
	 AND present=0 AND output='' AND output_present=0 AND built_hash=''
	 AND id NOT IN (SELECT value FROM json_each(?))
	 AND NOT EXISTS(SELECT 1 FROM managed WHERE source_id=sources.id)
	 AND NOT EXISTS(SELECT 1 FROM meta WHERE key='promotion:'||sources.id OR key LIKE 'deletion:'||sources.id||':%')
	 AND NOT EXISTS(SELECT 1 FROM busy j WHERE
	  (j.kind IN ('convert','move') AND json_extract(j.args,'$.id')=sources.id)
	  OR (j.dedup LIKE 'scan:prepare:%' AND json_extract(j.args,'$.dirs[0]')=sources.rel)
	  OR (j.kind='delete' AND sources.id IN (SELECT value FROM json_each(j.args,'$.ids')))
	  OR (j.kind='upgrade' AND sources.rel IN (SELECT value FROM json_each(j.args,'$.old') UNION ALL SELECT value FROM json_each(j.args,'$.new')))
	 )`
	result, err := tx.ExecContext(r.Context(), query, append(args, string(repairs))...)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	removed, err := result.RowsAffected()
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	skipped := count - int(removed)
	a.logger.Info("missing library records removed", "removed", removed, "skipped", skipped)
	respond(w, 200, map[string]int{"removed": int(removed), "skipped": skipped})
}
