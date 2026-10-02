package forge

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings(id INTEGER PRIMARY KEY CHECK(id=1), data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS admin(id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, password BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY, method TEXT NOT NULL, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS oidc_flows(state TEXT PRIMARY KEY, nonce TEXT NOT NULL, verifier TEXT NOT NULL, action TEXT NOT NULL, session TEXT NOT NULL, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sources(
 id INTEGER PRIMARY KEY, rel TEXT NOT NULL UNIQUE, hash TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL DEFAULT 0,
 artist TEXT NOT NULL DEFAULT '', album TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', track INTEGER NOT NULL DEFAULT 0, disc INTEGER NOT NULL DEFAULT 0,
 duration REAL NOT NULL DEFAULT 0, metadata TEXT NOT NULL DEFAULT '{}', present INTEGER NOT NULL DEFAULT 1,
 output TEXT NOT NULL DEFAULT '', built_hash TEXT NOT NULL DEFAULT '', built_profile TEXT NOT NULL DEFAULT '', output_present INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '', seen INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS source_hash ON sources(hash);
CREATE TABLE IF NOT EXISTS managed(path TEXT PRIMARY KEY, source_id INTEGER NOT NULL DEFAULT 0, kind TEXT NOT NULL, signature TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS jobs(
 id INTEGER PRIMARY KEY, kind TEXT NOT NULL, dedup TEXT NOT NULL, args TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, progress REAL NOT NULL DEFAULT 0, log TEXT NOT NULL DEFAULT '',
 not_before INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, updated INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS active_job ON jobs(dedup) WHERE state IN ('pending','running');
CREATE INDEX IF NOT EXISTS job_queue ON jobs(state,not_before);
CREATE INDEX IF NOT EXISTS job_history ON jobs(dedup,id DESC);
CREATE TABLE IF NOT EXISTS dirty_dirs(path TEXT PRIMARY KEY, updated INTEGER NOT NULL);
INSERT OR IGNORE INTO migrations(version,applied_at) VALUES(1,unixepoch());
UPDATE dirty_dirs SET updated=updated*1000000000 WHERE updated<1000000000000;
INSERT OR IGNORE INTO migrations(version,applied_at) VALUES(2,unixepoch());
PRAGMA user_version=2;
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 2 {
		db.Close()
		return nil, fmt.Errorf("database schema %d is newer than this application", version)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = tx.Exec(schema); err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const sourceCols = "id,rel,hash,size,mtime,artist,album,title,track,disc,duration,metadata,present,output,built_hash,built_profile,output_present,error"

type rowScanner interface{ Scan(...any) error }

func readSource(row rowScanner) (Source, error) {
	var s Source
	var metadata string
	err := row.Scan(&s.ID, &s.Rel, &s.Hash, &s.Size, &s.Mtime, &s.Artist, &s.Album, &s.Title, &s.Track, &s.Disc, &s.Duration, &metadata, &s.Present, &s.Output, &s.BuiltHash, &s.BuiltProfile, &s.OutputPresent, &s.Error)
	if err == nil {
		err = json.Unmarshal([]byte(metadata), &s.Metadata)
	}
	return s, err
}

func (a *App) source(id int64) (Source, error) {
	return readSource(a.db.QueryRow("SELECT "+sourceCols+" FROM sources WHERE id=?", id))
}
func (a *App) sourceRel(rel string) (Source, error) {
	return readSource(a.db.QueryRow("SELECT "+sourceCols+" FROM sources WHERE rel=?", rel))
}
func (a *App) allSources() ([]Source, error) {
	rows, err := a.db.Query("SELECT " + sourceCols + " FROM sources ORDER BY disc,track,rel")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Source{}
	for rows.Next() {
		s, err := readSource(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (a *App) enqueue(kind, key string, args any, manual bool) (int64, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return 0, err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	var state string
	err = tx.QueryRow("SELECT id,state FROM jobs WHERE dedup=? ORDER BY id DESC LIMIT 1", key).Scan(&id, &state)
	if err == nil && (state == "pending" || state == "running" || (state == "failed" && !manual && (kind == "convert" || kind == "move"))) {
		if kind == "scan" && (state == "pending" || state == "running") {
			var current string
			followup := "scan-followup:" + strconv.FormatInt(id, 10)
			if state == "pending" {
				err = tx.QueryRow("SELECT args FROM jobs WHERE id=?", id).Scan(&current)
			} else {
				err = tx.QueryRow("SELECT value FROM meta WHERE key=?", followup).Scan(&current)
				if err == sql.ErrNoRows {
					current = string(b)
					err = nil
				}
			}
			if err != nil {
				return 0, err
			}
			merged, err := mergeScan([]byte(current), b)
			if err != nil {
				return 0, err
			}
			if state == "pending" {
				_, err = tx.Exec("UPDATE jobs SET args=? WHERE id=?", merged, id)
			} else {
				_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", followup, merged)
			}
			if err != nil {
				return 0, err
			}
			return id, tx.Commit()
		}
		return id, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	now := time.Now().Unix()
	result, err := tx.Exec("INSERT INTO jobs(kind,dedup,args,created,updated) VALUES(?,?,?,?,?)", kind, key, string(b), now, now)
	if err != nil {
		return 0, err
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

const jobCols = "id,kind,dedup,args,state,attempts,progress,log,created,updated"

func readJob(row rowScanner) (Job, error) {
	var j Job
	var args string
	err := row.Scan(&j.ID, &j.Kind, &j.Key, &args, &j.State, &j.Attempts, &j.Progress, &j.Log, &j.Created, &j.Updated)
	j.Args = json.RawMessage(args)
	return j, err
}

func (a *App) claim(conversion bool) (Job, error) {
	operator := "<>"
	if conversion {
		operator = "="
	}
	tx, err := a.db.Begin()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	j, err := readJob(tx.QueryRow("SELECT "+jobCols+" FROM jobs candidate WHERE state='pending' AND kind "+operator+" 'convert' AND not_before<=? AND (kind NOT IN ('convert','move') OR NOT EXISTS(SELECT 1 FROM jobs active WHERE active.state='running' AND active.kind IN ('convert','move') AND json_extract(active.args,'$.id')=json_extract(candidate.args,'$.id'))) ORDER BY CASE kind WHEN 'scan' THEN 0 WHEN 'move' THEN 1 WHEN 'delete' THEN 2 ELSE 3 END,id LIMIT 1", time.Now().Unix()))
	if err != nil {
		return j, err
	}
	_, err = tx.Exec("UPDATE jobs SET state='running',updated=?,log='' WHERE id=? AND state='pending'", time.Now().Unix(), j.ID)
	if err != nil {
		return j, err
	}
	j.State = "running"
	return j, tx.Commit()
}

func (a *App) settings() (Settings, error) {
	var b string
	var s Settings
	err := a.db.QueryRow("SELECT data FROM settings WHERE id=1").Scan(&b)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(b), &s)
	return s, err
}
func (a *App) saveSettings(s Settings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = a.db.Exec("INSERT INTO settings(id,data) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", string(b))
	return err
}

// Full scans dominate scoped scans; verification cannot be lost while coalescing requests.
func mergeScan(first, second []byte) (string, error) {
	var a, b ScanRequest
	if err := json.Unmarshal(first, &a); err != nil {
		return "", err
	}
	if err := json.Unmarshal(second, &b); err != nil {
		return "", err
	}
	a.Verify = a.Verify || b.Verify
	if len(a.Dirs) == 0 || len(b.Dirs) == 0 {
		a.Dirs = nil
	} else {
		unique := map[string]bool{}
		for _, dir := range append(a.Dirs, b.Dirs...) {
			unique[dir] = true
		}
		a.Dirs = nil
		for dir := range unique {
			a.Dirs = append(a.Dirs, dir)
		}
		sort.Strings(a.Dirs)
	}
	raw, err := json.Marshal(a)
	return string(raw), err
}

func (a *App) completeJob(j Job, state string, attempts int, progress float64, message string, wait int) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args := string(j.Args)
	if j.Kind == "scan" {
		key := "scan-followup:" + strconv.FormatInt(j.ID, 10)
		var raw string
		err = tx.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if state == "success" {
				args = raw
				attempts = 0
			} else {
				args, err = mergeScan(j.Args, []byte(raw))
				if err != nil {
					return err
				}
			}
			state = "pending"
			progress = 0
			wait = 0
			if _, err = tx.Exec("DELETE FROM meta WHERE key=?", key); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec("UPDATE jobs SET args=?,state=?,attempts=?,progress=?,log=?,not_before=?,updated=? WHERE id=?", args, state, attempts, progress, message, time.Now().Unix()+int64(wait), time.Now().Unix(), j.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const markDirtySQL = "INSERT INTO dirty_dirs(path,updated) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET updated=max(dirty_dirs.updated+1,excluded.updated)"
