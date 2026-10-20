package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

const lyricsSearchPrefix = "lyrics-search:"
const lyricsSearchTTL = 7 * 24 * time.Hour

// Optional schema-2 metadata. Store public recording identifiers and lyrics,
// never account credentials, download access keys, or live selection tokens.
type cachedLyricsCandidate struct {
	Candidate     lyricsCandidate `json:"candidate"`
	RemoteID      string          `json:"remote_id,omitempty"`
	MID           string          `json:"mid,omitempty"`
	Hash          string          `json:"hash,omitempty"`
	TitleAliases  []string        `json:"title_aliases,omitempty"`
	ArtistAliases []string        `json:"artist_aliases,omitempty"`
	Text          *lyricsText     `json:"text,omitempty"`
}

type lyricsSearchRecord struct {
	Version      int                     `json:"version"`
	Fingerprint  string                  `json:"fingerprint"`
	Provider     string                  `json:"provider"`
	Query        string                  `json:"query"`
	Searched     int64                   `json:"searched"`
	Instrumental bool                    `json:"instrumental"`
	Candidates   []cachedLyricsCandidate `json:"candidates"`
}

func lyricsSearchQuery(source Source, query string) string {
	if query = strings.TrimSpace(query); query == "" {
		query = strings.TrimSpace(source.Title + " " + source.Artist)
	}
	return query
}

func lyricsSearchKey(target lyricsTarget, provider, query string) string {
	return lyricsSearchPrefix + strconv.FormatInt(target.Source.ID, 10) + ":" + digest(provider+"\x00"+lyricsSearchQuery(target.Source, query))
}

func lyricsSearchFingerprint(target lyricsTarget) string {
	s := target.Source
	raw, _ := json.Marshal([]any{target.Settings.Source, target.Settings.Output, s.ID, s.Rel, s.Hash, s.BuiltHash,
		s.Output, target.Signature, target.FilePresent, target.FileSignature, s.Title, s.Artist, s.Album, s.Duration})
	return digest(string(raw))
}

func decodeLyricsSearch(raw string, target lyricsTarget) (lyricsSearchRecord, bool) {
	var record lyricsSearchRecord
	if len(raw) > maxLyricsBytes || json.Unmarshal([]byte(raw), &record) != nil || record.Version != 1 ||
		record.Fingerprint != lyricsSearchFingerprint(target) || record.Searched > time.Now().Unix() ||
		time.Since(time.Unix(record.Searched, 0)) >= lyricsSearchTTL || len(record.Candidates) > 8 {
		return record, false
	}
	for _, provider := range lyricsProviders {
		if record.Provider == provider {
			return record, true
		}
	}
	return record, false
}

func (record lyricsSearchRecord) candidates(source Source) []lyricsCandidate {
	candidates := make([]lyricsCandidate, 0, len(record.Candidates))
	for _, cached := range record.Candidates {
		c := cached.Candidate
		c.Provider, c.remoteID, c.mid, c.hash = record.Provider, cached.RemoteID, cached.MID, cached.Hash
		c.titleAliases, c.artistAliases, c.text = cached.TitleAliases, cached.ArtistAliases, cached.Text
		candidates = append(candidates, c)
	}
	rankLyricsCandidates(candidates, source)
	for i := range candidates {
		candidates[i].Index = i
	}
	return candidates
}

func (a *App) cachedLyricsCandidates(ctx context.Context, target lyricsTarget, provider, query string, manual, refresh bool) ([]lyricsCandidate, bool, bool, int64, error) {
	if !refresh && target.Source.ID > 0 {
		raw, err := a.metaContext(ctx, lyricsSearchKey(target, provider, query))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, false, false, 0, err
		}
		if err == nil {
			if record, ok := decodeLyricsSearch(raw, target); ok {
				return record.candidates(target.Source), record.Instrumental, true, record.Searched, nil
			}
		}
	}
	candidates, instrumental, err := a.providerCandidates(ctx, provider, target.Source, query, manual)
	searched := time.Now().Unix()
	if err == nil {
		err = a.storeLyricsSearch(ctx, target, provider, query, candidates, instrumental, searched)
	}
	return candidates, instrumental, false, searched, err
}

func (a *App) storeLyricsSearch(ctx context.Context, target lyricsTarget, provider, query string, candidates []lyricsCandidate, instrumental bool, searched int64) error {
	if target.Source.ID < 1 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record := lyricsSearchRecord{Version: 1, Fingerprint: lyricsSearchFingerprint(target), Provider: provider,
		Query: lyricsSearchQuery(target.Source, query), Searched: searched, Instrumental: instrumental,
		Candidates: make([]cachedLyricsCandidate, 0, len(candidates))}
	for _, c := range candidates {
		record.Candidates = append(record.Candidates, cachedLyricsCandidate{Candidate: c, RemoteID: c.remoteID, MID: c.mid,
			Hash: c.hash, TitleAliases: c.titleAliases, ArtistAliases: c.artistAliases, Text: c.text})
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > maxLyricsBytes {
		return err // Large results remain usable through the bounded memory picker.
	}
	a.lyricsCacheMu.Lock()
	defer a.lyricsCacheMu.Unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := lyricsSearchKey(target, provider, query)
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw)); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT key,value FROM meta WHERE key LIKE 'lyrics-search:%'")
	if err != nil {
		return err
	}
	type entry struct {
		key      string
		size     int
		searched int64
	}
	var entries []entry
	for rows.Next() {
		var k, value string
		if err = rows.Scan(&k, &value); err != nil {
			break
		}
		var stored struct {
			Searched int64 `json:"searched"`
		}
		_ = json.Unmarshal([]byte(value), &stored)
		entries = append(entries, entry{k, len(value), stored.Searched})
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].searched == entries[j].searched {
			return entries[i].key == key && entries[j].key != key
		}
		return entries[i].searched > entries[j].searched
	})
	total := 0
	for i, e := range entries {
		total += e.size
		if i >= 512 || total > 32*maxLyricsBytes || time.Since(time.Unix(e.searched, 0)) >= lyricsSearchTTL {
			if _, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", e.key); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (a *App) restoreLyricsSearches(ctx context.Context, target lyricsTarget) ([]lyricsSearchRecord, error) {
	rows, err := a.reads.QueryContext(ctx, "SELECT value FROM meta WHERE key LIKE ?", lyricsSearchPrefix+strconv.FormatInt(target.Source.ID, 10)+":%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []lyricsSearchRecord
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if record, ok := decodeLyricsSearch(raw, target); ok {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Searched > records[j].Searched })
	return records, rows.Err()
}
