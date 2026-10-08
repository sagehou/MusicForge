package forge

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func mapLidarr(s Settings, path string) (string, error) {
	path = filepath.Clean(path)
	if s.LidarrPrefix != "" {
		prefix := filepath.Clean(s.LidarrPrefix)
		if !filepath.IsAbs(path) || !containsPath(prefix, path) {
			return "", errors.New("Lidarr path is outside the configured prefix")
		}
		rel, err := filepath.Rel(prefix, path)
		if err != nil {
			return "", err
		}
		path = filepath.Join(s.Source, rel)
	}
	if !filepath.IsAbs(path) || !containsPath(s.Source, path) {
		return "", errors.New("Lidarr path is outside the source library")
	}
	rel, err := filepath.Rel(s.Source, path)
	if err != nil {
		return "", err
	}
	// The worker resolves symlinks on the mount; webhook handling only queues paths.
	if err = validRelativePath(rel); err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(rel), ".flac") {
		return "", errors.New("Lidarr track path must be FLAC")
	}
	return rel, nil
}

func (a *App) webhook(w http.ResponseWriter, r *http.Request) {
	s, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if user, password, ok := r.BasicAuth(); ok && user == "musicforge" {
		secret = password
	}
	if s.WebhookHash == "" || !constantEqual(digest(secret), s.WebhookHash) {
		apiError(w, 401, errors.New("invalid webhook credentials"))
		return
	}
	var body struct {
		Event   string `json:"eventType"`
		Upgrade bool   `json:"isUpgrade"`
		Tracks  []struct {
			Path string `json:"path"`
		} `json:"trackFiles"`
		Deleted []struct {
			Path string `json:"path"`
		} `json:"deletedFiles"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	decoder := json.NewDecoder(r.Body)
	if err = decoder.Decode(&body); err != nil {
		apiError(w, 400, errors.New("invalid Lidarr payload"))
		return
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		apiError(w, 400, errors.New("invalid Lidarr payload"))
		return
	}
	if body.Event == "Test" {
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	if body.Event != "Download" {
		respond(w, 200, map[string]bool{"ignored": true})
		return
	}
	if !s.Enabled {
		apiError(w, 409, errors.New("library is disabled"))
		return
	}
	if len(body.Tracks) == 0 {
		apiError(w, 400, errors.New("Download event requires trackFiles"))
		return
	}
	upgrade := UpgradeRequest{}
	dirs := map[string]bool{}
	for _, track := range body.Tracks {
		rel, err := mapLidarr(s, track.Path)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		upgrade.New = append(upgrade.New, rel)
		dirs[filepath.Dir(rel)] = true
	}
	for _, track := range body.Deleted {
		rel, err := mapLidarr(s, track.Path)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		upgrade.Old = append(upgrade.Old, rel)
		dirs[filepath.Dir(rel)] = true
	}
	scan := ScanRequest{}
	for dir := range dirs {
		scan.Dirs = append(scan.Dirs, dir)
	}
	sort.Strings(scan.Dirs)
	b, _ := json.Marshal(scan)
	id, err := a.enqueue("scan", "lidarr:scan:"+digest(string(b)), scan, true)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if body.Upgrade && len(upgrade.Old) > 0 {
		b, _ := json.Marshal(upgrade)
		if _, err = a.enqueueTask("upgrade", "lidarr:upgrade:"+digest(string(b)), upgrade, true, id); err != nil {
			apiError(w, 500, err)
			return
		}
	}
	respond(w, 202, map[string]int64{"job_id": id})
}

func (a *App) finishUpgrade(ctx context.Context, r UpgradeRequest) error {
	if err := a.lockFiles(ctx); err != nil { return err }
	defer a.files.Unlock()
	s, err := a.settings()
	if err != nil {
		return err
	}
	if err = a.storageContext(ctx, s); err != nil {
		return err
	}
	if len(r.New) == 0 {
		return errors.New("upgrade has no replacement files")
	}
	newPaths := map[string]bool{}
	for _, rel := range r.New {
		source, err := a.sourceRel(rel)
		if err != nil {
			return later("Waiting for upgraded tracks to be indexed", 10)
		}
		if !source.Present || !source.OutputPresent || source.Error != "" || source.Hash == "" || source.BuiltHash != source.Hash || source.BuiltProfile != s.Encoding.Fingerprint() {
			return later("Waiting for all upgraded tracks to validate; failed tracks require manual retry", 10)
		}
		info, err := a.sourceStat(ctx, s.Source, rel)
		if err != nil || !sameStat(info, source) || time.Since(info.ModTime()) < 30*time.Second {
			return later("Waiting for upgraded sources to be stable and indexed", 10)
		}
		// Cleanup is destructive and infrequent: verify current bytes, not just cached state.
		hash, err := a.sourceHash(ctx, s.Source, rel, nil)
		if err != nil {
			return err
		}
		after, err := a.sourceStat(ctx, s.Source, rel)
		if err != nil || !sameStat(after, source) || hash != source.Hash {
			return later("Waiting for the current upgraded source content to validate", 10)
		}
		out, err := safePath(s.Output, source.Output)
		if err != nil {
			return err
		}
		if info, err := os.Stat(out); err != nil || !info.Mode().IsRegular() {
			return later("Waiting for missing upgraded artifacts", 10)
		}
		if err = a.owned(source.Output, source.ID); err != nil {
			return err
		}
		newPaths[rel] = true
	}
	for _, rel := range r.Old {
		if err := ctx.Err(); err != nil {
			return err
		}
		if newPaths[rel] {
			continue
		}
		source, err := a.sourceRel(rel)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if source.Output == "" {
			continue
		}
		if _, err = a.sourceStat(ctx, s.Source, rel); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = a.removeOwned(s, source.Output, source.ID); err != nil {
			return err
		}
		if _, err = a.db.Exec("UPDATE sources SET present=0,output='',built_hash='',built_profile='',output_present=0 WHERE id=?", source.ID); err != nil {
			return err
		}
	}
	return nil
}

type subsonicResponse struct {
	Response struct {
		Status        string `json:"status"`
		Type          string `json:"type"`
		ServerVersion string `json:"serverVersion"`
		ScanStatus    struct {
			Scanning bool   `json:"scanning"`
			Error    string `json:"error"`
		} `json:"scanStatus"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"subsonic-response"`
}

func (a *App) navRequest(ctx context.Context, s Settings, method string, targets []string) (subsonicResponse, error) {
	var result subsonicResponse
	salt := randomToken()
	hash := md5.Sum([]byte(s.NavPassword + salt))
	params := url.Values{"u": {s.NavUser}, "t": {hex.EncodeToString(hash[:])}, "s": {salt}, "v": {"1.16.1"}, "c": {"MusicForge"}, "f": {"json"}}
	for _, target := range targets {
		params.Add("target", target)
	}
	endpoint := s.NavURL + "/rest/" + method + ".view"
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return result, errors.New("invalid Navidrome URL")
	}
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return result, errors.New("Navidrome is unavailable or the request timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return result, fmt.Errorf("Navidrome returned HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&result); err != nil {
		return result, errors.New("invalid Navidrome response")
	}
	if result.Response.Status != "ok" {
		return result, fmt.Errorf("Navidrome API rejected the request (code %d)", result.Response.Error.Code)
	}
	return result, nil
}
func targetedVersion(version string) bool {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) < 2 {
		return false
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	return e1 == nil && e2 == nil && (major > 0 || minor >= 59)
}
func (a *App) refresh(ctx context.Context) error {
	s, err := a.settings()
	if err != nil {
		return err
	}
	if s.NavURL == "" {
		return errors.New("Navidrome is not configured")
	}
	if err = a.storageContext(ctx, s); err != nil {
		return err
	}
	signature := digest(fmt.Sprintf("%s:%s:%s:%d", s.NavURL, s.NavUser, s.NavPassword, s.NavLibrary))
	var previous struct {
		Signature string
		Dirty     map[string]int64
	}
	if raw, journalErr := a.meta("nav-refresh"); journalErr == nil {
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
		if previous.Signature == signature {
			return a.finishRefresh(ctx, s, previous.Dirty)
		}
		if _, err = a.db.Exec("DELETE FROM meta WHERE key='nav-refresh'"); err != nil {
			return err
		}
	} else if journalErr != sql.ErrNoRows {
		return journalErr
	}
	rows, err := a.db.Query("SELECT path,updated FROM dirty_dirs")
	if err != nil {
		return err
	}
	dirty := map[string]int64{}
	for rows.Next() {
		var dir string
		var stamp int64
		if err = rows.Scan(&dir, &stamp); err != nil {
			rows.Close()
			return err
		}
		dirty[dir] = stamp
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ping, err := a.navRequest(ctx, s, "ping", nil)
	if err != nil {
		return err
	}
	targets := []string{}
	if ping.Response.Type == "navidrome" && targetedVersion(ping.Response.ServerVersion) {
		unique := map[string]bool{}
		for dir := range dirty {
			for {
				path, err := safePath(s.Output, dir)
				if err != nil {
					return err
				}
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					break
				}
				if dir == "." {
					break
				}
				dir = filepath.Dir(dir)
			}
			unique[fmt.Sprintf("%d:%s", s.NavLibrary, filepath.ToSlash(dir))] = true
		}
		for target := range unique {
			targets = append(targets, target)
		}
		sort.Strings(targets)
	}
	status, err := a.navRequest(ctx, s, "getScanStatus", nil)
	if err != nil {
		return err
	}
	if status.Response.ScanStatus.Scanning {
		return later("Waiting for Navidrome's current scan to finish", 10)
	}
	if _, err = a.navRequest(ctx, s, "startScan", targets); err != nil {
		return err
	}
	previous.Signature = signature
	previous.Dirty = dirty
	raw, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	if err = a.setMeta("nav-refresh", string(raw)); err != nil {
		return err
	}
	return a.finishRefresh(ctx, s, dirty)
}

func (a *App) finishRefresh(ctx context.Context, s Settings, dirty map[string]int64) error {
	status, err := a.navRequest(ctx, s, "getScanStatus", nil)
	if err != nil {
		return err
	}
	if status.Response.ScanStatus.Scanning {
		return later("Waiting for Navidrome scan completion", 10)
	}
	if status.Response.ScanStatus.Error != "" {
		if _, err = a.db.Exec("DELETE FROM meta WHERE key='nav-refresh'"); err != nil {
			return err
		}
		return errors.New("Navidrome scan failed; directory changes are retained for retry")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for dir, stamp := range dirty {
		if _, err = tx.Exec("DELETE FROM dirty_dirs WHERE path=? AND updated=?", dir, stamp); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM meta WHERE key='nav-refresh'"); err != nil {
		return err
	}
	return tx.Commit()
}
