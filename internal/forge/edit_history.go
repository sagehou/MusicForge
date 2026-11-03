package forge

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type editRevision struct {
	Hash  string `json:"hash"`
	At    int64  `json:"at"`
	Bytes int    `json:"bytes"`
}

type editHistory struct {
	Version int            `json:"version"`
	Entries []editRevision `json:"entries"`
}

func historyKey(s Settings, kind, path string) string {
	return "edit-history:" + kind + ":" + digest(s.Source+"\x00"+s.Output+"\x00"+path)
}

func (a *App) readEditHistory(ctx context.Context, key string) (editHistory, error) {
	history := editHistory{Version: 1, Entries: []editRevision{}}
	raw, err := a.metaContext(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return history, nil
	}
	if err != nil {
		return history, err
	}
	err = json.Unmarshal([]byte(raw), &history)
	if err == nil && (history.Version != 1 || len(history.Entries) > 10) {
		err = errors.New("Invalid edit history")
	}
	return history, err
}

// Caller holds files. History is optional metadata; published ownership and
// journal formats stay unchanged and older releases preserve these references.
func (a *App) rememberEdit(ctx context.Context, s Settings, kind, path string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if kind != "lyrics" && kind != "artwork" {
		return errors.New("Invalid history kind")
	}
	limit := 8 * 1024 * 1024
	if kind == "lyrics" {
		limit = maxLyricsBytes
	}
	if len(data) > limit {
		return errors.New("Edit history snapshot exceeds the size limit")
	}
	key := historyKey(s, kind, path)
	history, err := a.readEditHistory(ctx, key)
	if err != nil {
		return err
	}
	hash := digest(string(data))
	if len(history.Entries) > 0 && history.Entries[0].Hash == hash {
		return nil
	}
	if err = a.saveEditBlob(ctx,kind,data);err!=nil{return err}
	history.Entries = append([]editRevision{{Hash: hash, At: time.Now().Unix(), Bytes: len(data)}}, history.Entries...)
	prune := len(history.Entries) > 10
	if prune { history.Entries = history.Entries[:10] }
	raw, _ := json.Marshal(history)
	if err = a.setMeta(key, string(raw)); err != nil { return err }
	if prune { return a.cleanEditHistory(ctx) }
	return nil
}

func (a *App) saveEditBlob(ctx context.Context,kind string,data []byte) error {
	if kind!="lyrics" && kind!="artwork" {return errors.New("Invalid history kind")}
	limit:=8*1024*1024;if kind=="lyrics"{limit=maxLyricsBytes}
	if len(data)==0 || len(data)>limit{return errors.New("Edit history snapshot exceeds the size limit")}
	hash:=digest(string(data))
	dir := filepath.Join(a.cfg.ConfigDir, "edit-history")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	name := hash + "." + kind
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		var actual string
		actual, err = fileHash(ctx, filepath.Join(dir, name))
		if err == nil && actual != hash {
			err = errors.New("Edit history snapshot changed")
		}
	} else if err == nil {
		_, writeErr := file.Write(data)
		err = errors.Join(writeErr, file.Sync(), file.Close())
	}
	if err != nil {
		return err
	}
	if err = syncDirectory(dir); err != nil {
		return err
	}
	return nil
}

func (a *App) readEditBlob(ctx context.Context,kind,hash string)([]byte,error){
	if !validDigest(hash) || kind!="lyrics" && kind!="artwork"{return nil,errors.New("Invalid edit snapshot")}
	root,err:=os.OpenRoot(filepath.Join(a.cfg.ConfigDir,"edit-history"));if err!=nil{return nil,err};defer root.Close()
	file,err:=openAuditionFile(root,hash+"."+kind);if err!=nil{return nil,err};defer file.Close()
	limit:=int64(8*1024*1024);if kind=="lyrics"{limit=maxLyricsBytes}
	if err=ctx.Err();err!=nil{return nil,err}
	data,err:=readLimited(file,limit);if err!=nil{return nil,err};if digest(string(data))!=hash{return nil,errors.New("Edit history snapshot changed")};return data,nil
}

func (a *App) cleanEditHistory(ctx context.Context) error {
	rows, err := a.reads.QueryContext(ctx, "SELECT key,value FROM meta WHERE key GLOB 'edit-history:*'")
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for rows.Next() {
		var key, raw string
		var history editHistory
		if err = rows.Scan(&key, &raw); err == nil {
			err = json.Unmarshal([]byte(raw), &history)
		}
		if err != nil || history.Version != 1 {
			rows.Close()
			return errors.New("Invalid edit history")
		}
		kind := "lyrics"
		if strings.HasPrefix(key, "edit-history:artwork:") {
			kind = "artwork"
		}
		for _, entry := range history.Entries {
			used[entry.Hash+"."+kind] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	operations,err:=a.reads.QueryContext(ctx,"SELECT value FROM meta WHERE key GLOB 'maintenance-operation:*'")
	if err!=nil{return err}
	for operations.Next(){var raw string;var operation maintenanceOperation
		if err=operations.Scan(&raw);err==nil{err=json.Unmarshal([]byte(raw),&operation)}
		if err!=nil{operations.Close();return err}
		if operation.Kind=="lyrics" || operation.Kind=="artwork"{for _,item:=range operation.Items{for _,hash:=range []string{item.BeforeHash,item.AfterHash,item.PriorOverrideHash}{if validDigest(hash){used[hash+"."+operation.Kind]=true}}}}
	}
	err=errors.Join(operations.Err(),operations.Close());if err!=nil{return err}
	dir := filepath.Join(a.cfg.ConfigDir, "edit-history")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err){return nil}
	if err != nil {
		return err
	}
	for _, file := range files {
		name := file.Name()
		if used[name] || !file.Type().IsRegular() || len(name) < 65 || !validDigest(name[:64]) || (name[64:] != ".lyrics" && name[64:] != ".artwork") {
			continue
		}
		if err = os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return syncDirectory(dir)
}

func validDigest(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("Response exceeds the size limit")
	}
	return data, err
}

func (a *App) getEditHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	kind := r.URL.Query().Get("kind")
	if err != nil || id < 1 || kind != "lyrics" && kind != "artwork" {
		apiError(w, 400, errors.New("Invalid history request"))
		return
	}
	source, err := a.source(id)
	if err != nil {
		apiError(w, 404, err)
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	path := source.Rel
	if kind == "artwork" {
		path = filepath.Join(filepath.Dir(source.Rel), "cover.jpg")
	}
	history, err := a.readEditHistory(r.Context(), historyKey(s, kind, path))
	if err != nil {
		apiError(w, 500, err)
		return
	}
	hash := r.URL.Query().Get("revision")
	if hash == "" {
		respond(w, 200, history)
		return
	}
	found := false
	for _, entry := range history.Entries {
		if entry.Hash == hash {
			found = true
		}
	}
	if !found || !validDigest(hash) {
		apiError(w, 404, errors.New("Revision is no longer available"))
		return
	}
	limit := int64(8 * 1024 * 1024)
	if kind == "lyrics" {
		limit = maxLyricsBytes
	}
	root, err := os.OpenRoot(filepath.Join(a.cfg.ConfigDir, "edit-history"))
	if err != nil {
		apiError(w, 409, err)
		return
	}
	defer root.Close()
	file, err := openAuditionFile(root, hash+"."+kind)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	defer file.Close()
	data, err := readLimited(file, limit)
	if err != nil || digest(string(data)) != hash {
		apiError(w, 409, errors.New("Edit history snapshot changed"))
		return
	}
	if kind == "lyrics" {
		respond(w, 200, map[string]string{"text": string(data)})
		return
	}
	respond(w, 200, map[string]string{"image": base64.StdEncoding.EncodeToString(data)})
}
