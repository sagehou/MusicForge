package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
)

type artifactDeleteItem struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Signature string `json:"signature"`
}

// Optional metadata leaves old delete jobs and their expired-only meaning intact.
// Older workers ignore this opt-in and retain their existing deletion checks.
type artifactDeletePlan struct {
	Version int                  `json:"version"`
	Output  string               `json:"output"`
	Items   []artifactDeleteItem `json:"items"`
}

func (a *App) requestArtifactDelete(w http.ResponseWriter, r *http.Request, body selection) {
	if body.All || len(body.IDs) == 0 || len(body.IDs) > 10000 {
		apiError(w, 400, errors.New("Select between 1 and 10000 tracks to delete their artifacts"))
		return
	}
	for _, id := range body.IDs {
		if id < 1 {
			apiError(w, 400, errors.New("Track IDs must be positive"))
			return
		}
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	ids, _ := json.Marshal(body.IDs)
	rows, err := a.reads.QueryContext(r.Context(), `SELECT s.id,s.output,m.signature FROM sources s
	 LEFT JOIN managed m ON m.path=s.output AND m.source_id=s.id AND m.kind='audio'
	 WHERE s.output<>'' AND s.id IN (SELECT value FROM json_each(?)) ORDER BY s.id`, string(ids))
	if err != nil {
		apiError(w, 500, err)
		return
	}
	plan := artifactDeletePlan{Version: 1, Output: s.Output}
	selected := []int64{}
	for rows.Next() {
		var item artifactDeleteItem
		var signature sql.NullString
		if err = rows.Scan(&item.ID, &item.Path, &signature); err != nil {
			break
		}
		if !signature.Valid {
			err = fmt.Errorf("Refusing unregistered or reassigned artifact %q", item.Path)
			break
		}
		item.Signature = signature.String
		plan.Items = append(plan.Items, item)
		selected = append(selected, item.ID)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		apiError(w, 409, err)
		return
	}
	if len(selected) == 0 {
		respond(w, 200, map[string]int{"deleted": 0})
		return
	}
	key := "delete:" + randomToken()
	raw, _ := json.Marshal(plan)
	// Persist authorization before enqueueing; a crash cannot turn an old delete
	// request into permission to remove healthy artifacts.
	if err = a.setMeta("artifact-delete:"+key, string(raw)); err != nil {
		apiError(w, 500, err)
		return
	}
	id, err := a.enqueue("delete", key, map[string]any{"ids": selected}, true)
	if err != nil {
		_, _ = a.db.Exec("DELETE FROM meta WHERE key=?", "artifact-delete:"+key)
	}
	a.jobResult(w, id, err)
}

func (a *App) deleteTaskContext(ctx context.Context, job Job, ids []int64) error {
	raw, err := a.metaContext(ctx, "artifact-delete:"+job.Key)
	if errors.Is(err, sql.ErrNoRows) {
		return a.deleteExpiredContext(ctx, ids)
	}
	if err != nil {
		return err
	}
	var plan artifactDeletePlan
	if err = json.Unmarshal([]byte(raw), &plan); err != nil {
		return err
	}
	if err = a.lockFiles(ctx); err != nil {
		return err
	}
	defer a.files.Unlock()
	s, err := a.settingsContext(ctx)
	if err != nil {
		return err
	}
	if plan.Version != 1 || plan.Output != s.Output {
		return errors.New("Artifact deletion settings changed; select the artifacts again")
	}
	if err = a.verifyOutputStorage(ctx, s); err != nil {
		return err
	}
	if err = a.ensureRecovery(ctx, s); err != nil {
		return err
	}
	if err = a.recoverPromotions(ctx, s); err != nil {
		return err
	}
	var failures []error
	for _, item := range plan.Items {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = a.deleteArtifact(ctx, s, item); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.Path, err))
		}
	}
	return errors.Join(failures...)
}

func (a *App) deleteArtifact(ctx context.Context, s Settings, item artifactDeleteItem) error {
	source, err := a.source(item.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var owner int64
	var signature string
	err = a.db.QueryRowContext(ctx, "SELECT source_id,signature FROM managed WHERE path=? AND kind='audio'", item.Path).Scan(&owner, &signature)
	if errors.Is(err, sql.ErrNoRows) && source.Output == "" {
		return nil // A completed deletion remains safe to retry.
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || owner != item.ID || source.Output != item.Path || signature != item.Signature {
		return errors.New("Artifact changed or ownership moved; select it again before deleting")
	}
	path, err := safePath(s.Output, item.Path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("Refusing to delete a non-regular artifact")
		}
		if signature != "" {
			hash, readErr := fileHash(ctx, path)
			if readErr != nil {
				return readErr
			}
			if hash != signature {
				return errors.New("Artifact was modified outside MusicForge; the file was kept")
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return a.removeOwned(s, item.Path, item.ID)
}
