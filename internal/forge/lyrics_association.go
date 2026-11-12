package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

type lyricsAssociation struct {
	Path        string `json:"path"`
	Text        string `json:"text"`
	Fingerprint string `json:"fingerprint"`
	Required    bool   `json:"required"`
	target      lyricsTarget
}

// Caller holds files. Review never adopts files or changes matching records.
func (a *App) captureLyricsAssociation(ctx context.Context, id int64) (review lyricsAssociation, err error) {
	target, err := a.captureLyricsArtifact(ctx, id, true)
	if err != nil {
		return review, err
	}
	review.Path = lyricsRel(target.Source.Output)
	var owners int
	err = a.reads.QueryRowContext(ctx, `SELECT count(*) FROM managed m JOIN sources s
	 ON s.id=m.source_id AND s.output=m.path WHERE m.kind='audio' AND s.output_present=1 AND m.path IN (?,?)`,
		outputRel(review.Path, "opus"), outputRel(review.Path, "mp3")).Scan(&owners)
	if err != nil {
		return review, err
	}
	if owners != 1 {
		return review, errors.New("Multiple songs share this LRC path; resolve the audio path conflict first")
	}
	root, err := os.OpenRoot(target.Settings.Output)
	if err != nil {
		return review, err
	}
	defer root.Close()
	file, err := openAuditionFile(root, review.Path)
	if err != nil {
		return review, errors.New("No regular output LRC is available to associate")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLyricsBytes+1))
	if err != nil {
		return review, err
	}
	review.Text = string(data)
	if _, err = normalizeLRC(review.Text); err != nil {
		return review, err
	}
	raw, err := a.metaContext(ctx, lyricsFileKey(review.Path))
	registered := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return review, err
	}
	target.FileSignature = digest(review.Text)
	target.FilePresent = registered && json.Unmarshal([]byte(raw), &target.File) == nil &&
		target.File.Path == review.Path && target.File.ID == id &&
		(target.File.Signature == target.FileSignature || target.File.Pending != "" && target.File.Pending == target.FileSignature)
	review.Required = !target.FilePresent || target.File.SourceHash != target.Source.BuiltHash
	status, err := a.optionalMeta(ctx, "lyrics-status:"+strconv.FormatInt(id, 10))
	if err != nil {
		return review, err
	}
	source := target.Source
	identity, _ := json.Marshal([]any{target.Settings.Source, target.Settings.Output, source.ID, source.Rel,
		source.Hash, source.BuiltHash, source.Output, target.Signature, registered, raw, status, target.FileSignature})
	review.Fingerprint, review.target = digest(string(identity)), target
	return review, nil
}

func (a *App) getLyricsAssociation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	if err = a.lockFiles(r.Context()); err != nil {
		apiError(w, 409, err)
		return
	}
	defer a.files.Unlock()
	review, err := a.captureLyricsAssociation(r.Context(), id)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 200, review)
}

func (a *App) saveLyricsAssociation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	if err = a.lockFiles(r.Context()); err != nil {
		apiError(w, 409, err)
		return
	}
	defer a.files.Unlock()
	review, err := a.captureLyricsAssociation(r.Context(), id)
	if err == nil && body.Fingerprint != review.Fingerprint {
		err = errors.New("Lyrics or song changed after association review; review again")
	}
	if err == nil && review.Required {
		err = a.associateLyrics(r.Context(), review)
	}
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 200, map[string]string{"text": review.Text})
}

// Only registry metadata changes; the reviewed LRC bytes remain untouched.
func (a *App) associateLyrics(ctx context.Context, review lyricsAssociation) error {
	target := review.target
	source := target.Source
	provider := "source"
	if target.FilePresent {
		provider = target.File.Provider
	}
	entry := lyricsFile{Path: review.Path, ID: source.ID, SourceHash: source.BuiltHash, Signature: target.FileSignature, Provider: provider}
	script := lyricsScriptRecord{Version: 1, Output: source.Output, AudioSignature: target.Signature,
		Path: review.Path, Signature: target.FileSignature, Script: lyricsScript(review.Text)}
	statusKey := "lyrics-status:" + strconv.FormatInt(source.ID, 10)
	raw, err := a.optionalMeta(ctx, statusKey)
	if err != nil {
		return err
	}
	var status LyricsStatus
	if !target.FilePresent || json.Unmarshal([]byte(raw), &status) != nil || status.State != "ready" || status.Output != source.Output || status.Signature != target.Signature {
		status = LyricsStatus{State: "ready", Output: source.Output, Signature: target.Signature,
			Path: review.Path, Provider: provider, Checked: time.Now().Unix(), Instrumental: review.Text == instrumentalLRC}
		data, _ := json.Marshal(status)
		raw = string(data)
	}
	fileJSON, _ := json.Marshal(entry)
	scriptJSON, _ := json.Marshal(script)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{lyricsFileKey(review.Path): string(fileJSON), lyricsScriptPrefix + strconv.FormatInt(source.ID, 10): string(scriptJSON), statusKey: raw} {
		if _, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.discardLyricsSearches(source.ID)
	return nil
}
