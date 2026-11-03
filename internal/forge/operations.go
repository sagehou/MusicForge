package forge

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type maintenanceOperation struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	SourceRoot string          `json:"source_root"`
	OutputRoot string          `json:"output_root"`
	Created    int64           `json:"created"`
	Items      []operationItem `json:"items"`
}

type operationItem struct {
	ID                int64             `json:"id"`
	Rel               string            `json:"path"`
	Output            string            `json:"output"`
	Hash              string            `json:"source_hash"`
	BuiltHash         string            `json:"built_hash"`
	Signature         string            `json:"audio_signature"`
	State             string            `json:"state"`
	Error             string            `json:"error,omitempty"`
	Before            map[string]string `json:"before,omitempty"`
	After             map[string]string `json:"after,omitempty"`
	PriorTags         map[string]string `json:"prior_tags,omitempty"`
	AppliedTags       map[string]string `json:"applied_tags,omitempty"`
	BeforeHash        string            `json:"before_hash,omitempty"`
	AfterHash         string            `json:"after_hash,omitempty"`
	BeforeMarker      string            `json:"before_marker,omitempty"`
	AfterMarker       string            `json:"after_marker,omitempty"`
	PriorOverride     string            `json:"prior_override,omitempty"`
	PriorOverrideHash string            `json:"prior_override_hash,omitempty"`
	AfterOverride     string            `json:"after_override,omitempty"`
	PriorLyrics       string            `json:"prior_lyrics,omitempty"`
	PriorScript       string            `json:"prior_script,omitempty"`
	PriorProvider     string            `json:"prior_provider,omitempty"`
	AfterProvider     string            `json:"after_provider,omitempty"`
	AfterLyrics       string            `json:"after_lyrics,omitempty"`
	AfterScript       string            `json:"after_script,omitempty"`
	Undoable          bool              `json:"undoable"`
}

func operationTrack(source Source, signature string) operationItem {
	return operationItem{ID: source.ID, Rel: source.Rel, Output: source.Output, Hash: source.Hash, BuiltHash: source.BuiltHash, Signature: signature, State: "prepared", Undoable: true}
}

func (a *App) readOperation(ctx context.Context, id string) (maintenanceOperation, error) {
	var operation maintenanceOperation
	if !validDigest(id) {
		return operation, errors.New("Invalid maintenance operation")
	}
	raw, err := a.metaContext(ctx, "maintenance-operation:"+id)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &operation)
	}
	if err == nil {
		err = validateOperation(operation, id)
	}
	return operation, err
}

func validateOperation(operation maintenanceOperation, id string) error {
	if operation.Version != 1 || !validDigest(id) || operation.ID != id || len(operation.Items) == 0 || len(operation.Items) > 1000 || (operation.Kind != "tags" && operation.Kind != "lyrics" && operation.Kind != "artwork") {
		return errors.New("Invalid maintenance operation")
	}
	return nil
}

func (a *App) writeOperation(operation maintenanceOperation) error {
	raw, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	return a.setMeta("maintenance-operation:"+operation.ID, string(raw))
}

// Caller holds files. Persist intent before publication; interrupted entries
// remain visible and are never eligible for automatic replay or undo.
func (a *App) prepareOperation(ctx context.Context, s Settings, kind, batch string, item operationItem) (string, int, error) {
	operation := maintenanceOperation{Version: 1, ID: batch, Kind: kind, SourceRoot: s.Source, OutputRoot: s.Output, Created: time.Now().UnixNano()}
	if operation.ID == "" {
		operation.ID = randomToken()
	} else {
		prior, err := a.readOperation(ctx, batch)
		if err == nil {
			operation = prior
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", 0, err
		}
	}
	if operation.Kind != kind || operation.SourceRoot != s.Source || operation.OutputRoot != s.Output || len(operation.Items) >= 1000 {
		return "", 0, errors.New("Maintenance operation changed or exceeds 1000 items")
	}
	index := len(operation.Items)
	operation.Items = append(operation.Items, item)
	if err := a.writeOperation(operation); err != nil {
		return "", 0, err
	}
	return operation.ID, index, nil
}

func (a *App) finishOperation(id string, index int, signature string, operationErr error) {
	operation, err := a.readOperation(context.Background(), id)
	if err != nil || index < 0 || index >= len(operation.Items) {
		a.logger.Warn("operation record unavailable", "error", err)
		return
	}
	item := &operation.Items[index]
	item.State = "applied"
	if signature != "" {
		item.Signature = signature
	}
	if operationErr != nil {
		item.State, item.Error = "failed", operationErr.Error()
	}
	if operationErr == nil && operation.Kind == "lyrics" {
		item.AfterLyrics, err = a.optionalMeta(context.Background(), "lyrics-status:"+strconv.FormatInt(item.ID, 10))
		if err == nil {
			item.AfterScript, err = a.optionalMeta(context.Background(), lyricsScriptPrefix+strconv.FormatInt(item.ID, 10))
		}
		if err != nil {
			a.logger.Warn("operation result annotation unavailable", "error", err)
			return
		}
	}
	if operationErr == nil && operation.Kind == "artwork" {
		item.AfterOverride, err = a.optionalMeta(context.Background(), artworkOverrideKey(filepath.Join(filepath.Dir(item.Rel), "cover.jpg")))
		if err != nil {
			a.logger.Warn("operation result annotation unavailable", "error", err)
			return
		}
	}
	if err = a.writeOperation(operation); err != nil {
		a.logger.Warn("operation record save failed", "error", err)
	}
	s := Settings{Source: operation.SourceRoot, Output: operation.OutputRoot}
	if err = a.pruneOperations(context.Background(), s); err != nil {
		a.logger.Warn("operation record cleanup failed", "error", err)
	}
}

func (a *App) operations(ctx context.Context, s Settings) ([]maintenanceOperation, error) {
	rows, err := a.reads.QueryContext(ctx, "SELECT value FROM meta WHERE key GLOB 'maintenance-operation:*'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []maintenanceOperation{}
	for rows.Next() {
		var raw string
		var operation maintenanceOperation
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &operation) != nil || validateOperation(operation, operation.ID) != nil {
			return nil, errors.New("Invalid maintenance operation")
		}
		if operation.SourceRoot == s.Source && operation.OutputRoot == s.Output {
			result = append(result, operation)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Created > result[j].Created })
	return result, rows.Err()
}

func (a *App) pruneOperations(ctx context.Context, s Settings) error {
	operations, err := a.operations(ctx, s)
	if err != nil {
		return err
	}
	removed := false
	for _, operation := range operations[min(50, len(operations)):] {
		prepared := false
		for _, item := range operation.Items {
			prepared = prepared || item.State == "prepared"
		}
		if !prepared {
			if _, err = a.db.ExecContext(ctx, "DELETE FROM meta WHERE key=?", "maintenance-operation:"+operation.ID); err != nil {
				return err
			}
			removed = true
		}
	}
	if removed {
		return a.cleanEditHistory(ctx)
	}
	return nil
}

func (a *App) getOperations(w http.ResponseWriter, r *http.Request) {
	s, err := a.settingsContext(r.Context())
	var result []maintenanceOperation
	if err == nil {
		result, err = a.operations(r.Context(), s)
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, result)
}

func (a *App) getOperationSnapshot(w http.ResponseWriter, r *http.Request) {
	operation, err := a.readOperation(r.Context(), r.PathValue("operation"))
	index, indexErr := strconv.Atoi(r.URL.Query().Get("item"))
	s, settingsErr := a.settingsContext(r.Context())
	side := r.URL.Query().Get("side")
	if err != nil || indexErr != nil || settingsErr != nil || index < 0 || index >= len(operation.Items) || operation.SourceRoot != s.Source || operation.OutputRoot != s.Output || (operation.Kind != "lyrics" && operation.Kind != "artwork") || (side != "before" && side != "after") {
		apiError(w, 404, errors.New("Operation snapshot is unavailable"))
		return
	}
	hash := operation.Items[index].BeforeHash
	if side == "after" {
		hash = operation.Items[index].AfterHash
	}
	if hash == "" {
		respond(w, 200, map[string]string{})
		return
	}
	data, err := a.readEditBlob(r.Context(), operation.Kind, hash)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	if operation.Kind == "lyrics" {
		respond(w, 200, map[string]string{"text": string(data)})
	} else {
		respond(w, 200, map[string]string{"image": base64.StdEncoding.EncodeToString(data)})
	}
}

func (a *App) recoverOperations() error {
	rows, err := a.reads.Query("SELECT key,value FROM meta WHERE key GLOB 'maintenance-operation:*'")
	if err != nil {
		return err
	}
	operations := []maintenanceOperation{}
	for rows.Next() {
		var key, raw string
		var operation maintenanceOperation
		if err = rows.Scan(&key, &raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &operation); err != nil {
			break
		}
		if err = validateOperation(operation, operation.ID); err != nil {
			break
		}
		if key != "maintenance-operation:"+operation.ID {
			err = errors.New("Invalid maintenance operation key")
			break
		}
		operations = append(operations, operation)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	for _, operation := range operations {
		changed := false
		for i := range operation.Items {
			if operation.Items[i].State == "prepared" {
				operation.Items[i].State = "interrupted"
				changed = true
			}
		}
		if changed {
			if err = a.writeOperation(operation); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) operationSource(ctx context.Context, s Settings, operation maintenanceOperation, item operationItem) (Source, error) {
	source, err := a.source(item.ID)
	if err != nil {
		return source, err
	}
	settings, err := a.settingsContext(ctx)
	if err != nil {
		return source, err
	}
	var signature string
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'", item.ID, item.Output).Scan(&signature)
	if err != nil || !settings.Enabled || settings.Source != operation.SourceRoot || settings.Output != operation.OutputRoot || s.Source != settings.Source || s.Output != settings.Output || source.Rel != item.Rel || source.Output != item.Output || source.Hash != item.Hash || source.BuiltHash != item.BuiltHash || !source.OutputPresent || signature != item.Signature {
		return source, errors.New("Maintenance undo conflict: track or library changed; current files preserved")
	}
	return source, a.verifyOutputStorage(ctx, settings)
}

func (a *App) undoOperationItem(ctx context.Context, operation maintenanceOperation, index int) error {
	item := operation.Items[index]
	s, err := a.settingsContext(ctx)
	if err != nil {
		return err
	}
	if operation.Kind == "tags" {
		source, err := a.operationSource(ctx, s, operation, item)
		if err != nil {
			return err
		}
		changes, err := a.outputTagChanges(ctx, s, source, source.BuiltHash)
		if err != nil {
			return err
		}
		if !maps.Equal(changes, item.AppliedTags) {
			return errors.New("Maintenance undo conflict: tag correction policy changed; current files preserved")
		}
		prior := item.PriorTags
		if prior == nil {
			prior = map[string]string{}
		}
		_, err = a.editOutputTags(ctx, s, tagPreviewTrack{ID: item.ID, Path: item.Output, Source: source, Signature: item.Signature, After: item.Before, Restore: &prior, SkipAudit: true})
		return err
	}
	if err = a.lockFiles(ctx); err != nil {
		return err
	}
	defer a.files.Unlock()
	source, err := a.operationSource(ctx, s, operation, item)
	if err != nil {
		return err
	}
	switch operation.Kind {
	case "lyrics":
		entry, text, present, err := a.readLyricsFile(ctx, s, lyricsRel(source.Output))
		if err != nil {
			return err
		}
		status, err := a.optionalMeta(ctx, "lyrics-status:"+strconv.FormatInt(item.ID, 10))
		if err != nil {
			return err
		}
		script, err := a.optionalMeta(ctx, lyricsScriptPrefix+strconv.FormatInt(item.ID, 10))
		if err != nil {
			return err
		}
		if !present || digest(text) != item.AfterHash || entry.Provider != item.AfterProvider || status != item.AfterLyrics || script != item.AfterScript {
			return errors.New("Maintenance undo conflict: lyrics changed; current file preserved")
		}
		if item.BeforeHash == "" {
			err = a.removeLyricsFile(ctx, s, lyricsRel(source.Output))
		} else {
			var data []byte
			data, err = a.readEditBlob(ctx, "lyrics", item.BeforeHash)
			if err == nil {
				err = a.writeLyricsFile(ctx, s, source, string(data), item.PriorProvider)
			}
			if err == nil {
				err = a.rememberEdit(ctx, s, "lyrics", source.Rel, data)
			}
		}
		if err == nil {
			err = a.restoreOptionalMeta("lyrics-status:"+strconv.FormatInt(item.ID, 10), item.PriorLyrics)
		}
		if err == nil {
			err = a.restoreOptionalMeta(lyricsScriptPrefix+strconv.FormatInt(item.ID, 10), item.PriorScript)
		}
		return err
	case "artwork":
		path := filepath.Join(filepath.Dir(source.Rel), "cover.jpg")
		out, err := safePath(s.Output, path)
		if err != nil {
			return err
		}
		var marker string
		err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE path=? AND kind='cover'", path).Scan(&marker)
		actual, hashErr := fileHash(ctx, out)
		if err != nil || hashErr != nil || actual != item.AfterHash || marker != item.AfterMarker {
			return errors.New("Maintenance undo conflict: cover changed; current file preserved")
		}
		override, err := a.optionalMeta(ctx, artworkOverrideKey(path))
		if err != nil {
			return err
		}
		if override != item.AfterOverride {
			return errors.New("Maintenance undo conflict: cover changed; current file preserved")
		}
		// Check every snapshot before changing the current cover or override policy.
		if item.PriorOverrideHash != "" {
			data, readErr := a.readEditBlob(ctx, "artwork", item.PriorOverrideHash)
			if readErr != nil {
				return readErr
			}
			if _, err = a.storeArtworkBlob(ctx, data); err != nil {
				return err
			}
		}
		if item.BeforeHash == "" {
			err = a.publishArtwork(ctx, s, filepath.Dir(path), "", "", false)
		} else {
			data, readErr := a.readEditBlob(ctx, "artwork", item.BeforeHash)
			if readErr != nil {
				return readErr
			}
			if err = a.rememberEdit(ctx, s, "artwork", path, data); err != nil {
				return err
			}
			input := filepath.Join(a.cfg.ConfigDir, "edit-history", item.BeforeHash+".artwork")
			err = a.publishArtwork(ctx, s, filepath.Dir(path), input, item.BeforeMarker, false, true)
		}
		if err != nil {
			return err
		}
		if err = a.restoreOptionalMeta(artworkOverrideKey(path), item.PriorOverride); err != nil {
			return err
		}
		return a.cleanArtworkOverrides(ctx)
	}
	return errors.New("This operation requires manual restoration")
}

func (a *App) restoreOptionalMeta(key, raw string) error {
	if raw != "" {
		return a.setMeta(key, raw)
	}
	_, err := a.db.Exec("DELETE FROM meta WHERE key=?", key)
	return err
}

func (a *App) optionalMeta(ctx context.Context, key string) (string, error) {
	raw, err := a.metaContext(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return raw, err
}

func (a *App) startUndoOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []int `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	operation, err := a.readOperation(r.Context(), r.PathValue("operation"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	if len(body.Items) == 0 || len(body.Items) > 1000 {
		apiError(w, 400, errors.New("Select between 1 and 1000 operation items"))
		return
	}
	seen := map[int]bool{}
	for _, index := range body.Items {
		if index < 0 || index >= len(operation.Items) || seen[index] || operation.Items[index].State != "applied" || !operation.Items[index].Undoable {
			apiError(w, 409, errors.New("Selected operation item cannot be undone"))
			return
		}
		seen[index] = true
	}
	err = a.runLibraryTool("undo", len(body.Items), func(ctx context.Context) {
		for _, index := range body.Items {
			if ctx.Err() != nil {
				break
			}
			item := operation.Items[index]
			a.toolCurrent("undo", item.Rel)
			current, readErr := a.readOperation(ctx, operation.ID)
			if readErr == nil && (index >= len(current.Items) || current.Items[index].State != "applied") {
				readErr = errors.New("Operation was already undone")
			}
			if readErr == nil {
				readErr = a.waitOutputMaintenanceCapacity(ctx)
			}
			if readErr == nil {
				readErr = a.undoOperationItem(ctx, operation, index)
			}
			if readErr == nil {
				a.files.Lock()
				current, readErr = a.readOperation(ctx, operation.ID)
				if readErr == nil {
					current.Items[index].State = "undone"
					readErr = a.writeOperation(current)
				}
				a.files.Unlock()
			}
			if ctx.Err() != nil {
				break
			}
			a.toolProgress("undo", Source{ID: item.ID}, readErr == nil, readErr)
		}
	})
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 202, map[string]bool{"ok": true})
}
