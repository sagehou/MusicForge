package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type artworkEditState struct {
	artworkMu       sync.Mutex
	artworkChoices  map[string]artworkChoice
	artworkSearched time.Time
}

type artworkChoice struct {
	Created   time.Time
	Settings  Settings
	ID        int64
	Source    string
	Path      string
	Signature string
	Hash      string
	Present   bool
}

type artworkOverride struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
}

func artworkOverrideKey(path string) string {
	return "artwork-override:" + digest(filepath.ToSlash(path))
}

func (a *App) overrideArtwork(ctx context.Context, dir string) (string, string, bool, error) {
	rel := filepath.Join(dir, "cover.jpg")
	raw, err := a.metaContext(ctx, artworkOverrideKey(rel))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	var entry artworkOverride
	if err == nil {
		err = json.Unmarshal([]byte(raw), &entry)
	}
	if err != nil {
		return "", "", false, err
	}
	if entry.Version != 1 || entry.Path != rel || len(entry.Hash) != 64 || strings.Trim(entry.Hash, "0123456789abcdef") != "" {
		return "", "", false, errors.New("Invalid artwork override")
	}
	path := filepath.Join(a.cfg.ConfigDir, "artwork-overrides", entry.Hash+".jpg")
	hash, err := fileHash(ctx, path)
	if err != nil {
		return "", "", false, err
	}
	if hash != entry.Hash {
		return "", "", false, errors.New("Artwork override changed; upload the cover again")
	}
	return path, "external:" + hash, true, nil
}

func (a *App) captureArtworkChoice(ctx context.Context, id int64) (artworkChoice, error) {
	choice := artworkChoice{Created: time.Now(), ID: id}
	if err := a.lockFiles(ctx); err != nil {
		return choice, err
	}
	defer a.files.Unlock()
	s, err := a.settingsContext(ctx)
	if err != nil {
		return choice, err
	}
	if !s.Enabled {
		return choice, errors.New("library is disabled")
	}
	if err = a.verifyOutputStorage(ctx, s); err != nil {
		return choice, err
	}
	source, err := a.source(id)
	if err != nil {
		return choice, err
	}
	choice.Settings, choice.Source = s, source.Rel
	choice.Path = filepath.Join(filepath.Dir(source.Rel), "cover.jpg")
	var kind string
	err = a.reads.QueryRowContext(ctx, "SELECT kind,signature FROM managed WHERE path=?", choice.Path).Scan(&kind, &choice.Signature)
	registered := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return choice, err
	}
	if registered && (kind != "cover" || choice.Signature == "") {
		return choice, errors.New("Artwork ownership is unavailable; scan the album first")
	}
	path, err := safePath(s.Output, choice.Path)
	if err != nil {
		return choice, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return choice, nil
	}
	if err != nil {
		return choice, err
	}
	if !registered || !info.Mode().IsRegular() || info.Size() > 20*1024*1024 {
		return choice, errors.New("unregistered artwork path conflict")
	}
	choice.Present = true
	choice.Hash, err = fileHash(ctx, path)
	return choice, err
}

func (a *App) getArtworkEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	choice, err := a.captureArtworkChoice(r.Context(), id)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	token := randomToken()
	a.artworkMu.Lock()
	if a.artworkChoices == nil {
		a.artworkChoices = map[string]artworkChoice{}
	}
	for key, entry := range a.artworkChoices {
		if time.Since(entry.Created) >= 10*time.Minute {
			delete(a.artworkChoices, key)
		}
	}
	if len(a.artworkChoices) >= 16 {
		oldest := ""
		for key, entry := range a.artworkChoices {
			if oldest == "" || entry.Created.Before(a.artworkChoices[oldest].Created) {
				oldest = key
			}
		}
		delete(a.artworkChoices, oldest)
	}
	a.artworkChoices[token] = choice
	a.artworkMu.Unlock()
	_, overrideErr := a.metaContext(r.Context(), artworkOverrideKey(choice.Path))
	respond(w, 200, map[string]any{"token": token, "override": overrideErr == nil})
}

func canonicalArtwork(data []byte) ([]byte, error) {
	if len(data) > 8*1024*1024 {
		return nil, errors.New("Artwork must be JPEG or PNG, at most 8 MiB and 4096 pixels per side")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 16*1024*1024 {
		return nil, errors.New("Artwork must be JPEG or PNG, at most 8 MiB and 4096 pixels per side")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	if buffer.Len() > 8*1024*1024 {
		return nil, errors.New("Artwork JPEG exceeds 8 MiB; resize the image before uploading")
	}
	return buffer.Bytes(), nil
}

func (a *App) saveArtworkEdit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Image string `json:"image,omitempty"`
	}
	if !decode(w, r, &body, 12*1024*1024) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.artworkMu.Lock()
	choice, ok := a.artworkChoices[body.Token]
	a.artworkMu.Unlock()
	if err != nil || !ok || choice.ID != id || time.Since(choice.Created) >= 10*time.Minute {
		apiError(w, 409, errors.New("Artwork edit expired; reopen the editor"))
		return
	}
	var data []byte
	if r.Method != http.MethodDelete {
		data, err = base64.StdEncoding.DecodeString(body.Image)
		if err == nil {
			data, err = canonicalArtwork(data)
		}
		if err != nil {
			apiError(w, 400, errors.New("Artwork must be JPEG or PNG, at most 8 MiB and 4096 pixels per side"))
			return
		}
	}
	current, err := a.captureArtworkChoice(r.Context(), id)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	if err = a.lockFiles(r.Context()); err != nil {
		apiError(w, 503, err)
		return
	}
	defer a.files.Unlock()
	// Check again under the publication lock: a scan may have changed this album.
	source, err := a.source(id)
	s, settingsErr := a.settingsContext(r.Context())
	var signature string
	readErr := a.reads.QueryRowContext(r.Context(), "SELECT signature FROM managed WHERE path=? AND kind='cover'", choice.Path).Scan(&signature)
	if err != nil || settingsErr != nil || source.Rel != choice.Source || s.Source != choice.Settings.Source || s.Output != choice.Settings.Output || !s.Enabled || current.Present != choice.Present || current.Hash != choice.Hash || current.Signature != choice.Signature || readErr != nil && !errors.Is(readErr, sql.ErrNoRows) || signature != choice.Signature {
		apiError(w, 409, errors.New("Artwork or library settings changed; reopen the editor"))
		return
	}
	if err = a.verifyOutputStorage(r.Context(), s); err != nil {
		apiError(w, 409, err)
		return
	}
	if choice.Present {
		path, pathErr := safePath(s.Output, choice.Path)
		if pathErr != nil {
			apiError(w, 409, pathErr)
			return
		}
		actual, hashErr := fileHash(r.Context(), path)
		if hashErr != nil || actual != choice.Hash {
			apiError(w, 409, errors.New("Artwork or library settings changed; reopen the editor"))
			return
		}
		previous, readErr := os.ReadFile(path)
		if readErr == nil {
			readErr = a.rememberEdit(r.Context(), s, "artwork", choice.Path, previous)
		}
		if readErr != nil {
			apiError(w, 500, readErr)
			return
		}
	}
	item:=operationTrack(source,"")
	_ = a.reads.QueryRowContext(r.Context(),"SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'",source.ID,source.Output).Scan(&item.Signature)
	item.Undoable=r.Method!=http.MethodDelete && source.OutputPresent && validDigest(item.Signature)
	item.BeforeMarker=choice.Signature
	if choice.Present{item.BeforeHash=choice.Hash}
	if len(data)>0{item.AfterHash=digest(string(data));item.AfterMarker="external:"+item.AfterHash;if err=a.saveEditBlob(r.Context(),"artwork",data);err!=nil{apiError(w,500,err);return}}
	item.PriorOverride,err=a.optionalMeta(r.Context(),artworkOverrideKey(choice.Path));if err!=nil{apiError(w,500,err);return}
	if item.PriorOverride!=""{
		path,_,_,readErr:=a.overrideArtwork(r.Context(),filepath.Dir(choice.Path));if readErr!=nil{apiError(w,409,readErr);return}
		prior,readErr:=os.ReadFile(path);if readErr==nil{readErr=a.saveEditBlob(r.Context(),"artwork",prior)};if readErr!=nil{apiError(w,500,readErr);return};item.PriorOverrideHash=digest(string(prior))
	}
	operation,index,prepareErr:=a.prepareOperation(r.Context(),s,"artwork","",item);if prepareErr!=nil{apiError(w,500,prepareErr);return}
	operationErr:=errors.New("Artwork edit did not complete")
	defer func(){if err!=nil{operationErr=err};a.finishOperation(operation,index,"",operationErr)}()
	if r.Method == http.MethodDelete {
		_, err = a.db.ExecContext(r.Context(), "DELETE FROM meta WHERE key=?", artworkOverrideKey(choice.Path))
	} else {
		var path string
		path,err=a.storeArtworkBlob(r.Context(),data)
		if err == nil {
			hash := digest(string(data))
			if err == nil {
				raw, _ := json.Marshal(artworkOverride{Version: 1, Path: choice.Path, Hash: hash})
				err = a.setMeta(artworkOverrideKey(choice.Path), string(raw))
			}
			if err == nil {
				err = a.publishArtwork(r.Context(), s, filepath.Dir(choice.Path), path, "external:"+hash, false, true)
			}
		}
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if len(data) > 0 {
		if err = a.rememberEdit(r.Context(), s, "artwork", choice.Path, data); err != nil {
			apiError(w, 500, err)
			return
		}
	}
	if err = a.cleanArtworkOverrides(r.Context()); err != nil {
		a.logger.Warn("artwork override cleanup failed", "error", err)
	}
	a.artworkMu.Lock()
	delete(a.artworkChoices, body.Token)
	a.artworkMu.Unlock()
	if r.Method == http.MethodDelete {
		job, queueErr := a.enqueue("scan", "scan:manual:false", ScanRequest{Dirs: []string{filepath.Dir(choice.Source)}}, true)
		operationErr=queueErr
		a.jobResult(w, job, queueErr)
		return
	}
	operationErr=nil
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) storeArtworkBlob(ctx context.Context,data []byte)(string,error){
	dir:=filepath.Join(a.cfg.ConfigDir,"artwork-overrides")
	if err:=os.MkdirAll(dir,0700);err!=nil{return "",err}
	hash:=digest(string(data));path:=filepath.Join(dir,hash+".jpg")
	file,err:=os.OpenFile(path,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600)
	if errors.Is(err,os.ErrExist){actual,readErr:=fileHash(ctx,path);err=readErr;if err==nil && actual!=hash{err=errors.New("Artwork override changed; upload the cover again")}}else if err==nil{_,writeErr:=file.Write(data);err=errors.Join(writeErr,file.Sync(),file.Close())}
	if err==nil{err=syncDirectory(dir)}
	return path,err
}

// Only this feature's content-addressed blobs are eligible for cleanup. Caller
// holds files, so another album cannot publish a reference during collection.
func (a *App) cleanArtworkOverrides(ctx context.Context) error {
	rows, err := a.reads.QueryContext(ctx, "SELECT value FROM meta WHERE key GLOB 'artwork-override:*'")
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for rows.Next() {
		var raw string
		var entry artworkOverride
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &entry)
		}
		if err != nil {
			rows.Close()
			return err
		}
		used[entry.Hash+".jpg"] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	dir := filepath.Join(a.cfg.ConfigDir, "artwork-overrides")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) != 68 || !strings.HasSuffix(name, ".jpg") || strings.Trim(name[:64], "0123456789abcdef") != "" || used[name] || !entry.Type().IsRegular() {
			continue
		}
		if err = os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return syncDirectory(dir)
}
