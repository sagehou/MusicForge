package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
)

type libraryIndexState struct {
	libraryMu    sync.Mutex
	libraryWatch *sql.Conn
	libraryIndex *libraryIndex
	libraryPages map[string]*libraryPageCache
}

type libraryIndex struct {
	version  int64
	repairs  string
	revision string
	sources  []Source
	byID     map[int64]int
}

type libraryPageCache struct {
	page libraryPage
	raw  []byte
	etag string
}

// Keep one read connection so SQLite's data_version can observe every committed
// writer, including older schema-2 code, without triggers or persisted counters.
func (a *App) cachedLibraryIndex(ctx context.Context) (*libraryIndex, error) {
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	if a.libraryWatch == nil {
		conn, err := a.reads.Conn(ctx)
		if err != nil {
			return nil, err
		}
		a.libraryWatch, a.libraryIndex = conn, nil
	}
	var version int64
	if err := a.libraryWatch.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		a.libraryWatch.Close()
		a.libraryWatch, a.libraryIndex = nil, nil
		return nil, err
	}
	a.repairMu.Lock()
	repairs, err := json.Marshal(a.repairs)
	a.repairMu.Unlock()
	if err != nil {
		return nil, err
	}
	if cached := a.libraryIndex; cached != nil && cached.version == version && cached.repairs == string(repairs) {
		return cached, nil
	}
	list, err := a.sourcesWithStatusContext(ctx)
	if err != nil {
		return nil, err
	}
	if err = a.libraryArtworkURLs(ctx, list); err != nil {
		return nil, err
	}
	if err = a.annotateLyrics(ctx, list); err != nil {
		return nil, err
	}
	index := &libraryIndex{version: version, repairs: string(repairs), revision: randomToken(), sources: list, byID: make(map[int64]int, len(list))}
	for i, source := range list {
		index.byID[source.ID] = i
	}
	// Store the version observed before reading: a concurrent write must cause
	// the next request to refresh, rather than mark an older snapshot as current.
	a.libraryIndex = index
	return index, nil
}

func (a *App) browseLibrary(w http.ResponseWriter, r *http.Request) {
	query, err := parseLibraryQuery(r.URL.Query())
	if err != nil {
		apiError(w, 400, err)
		return
	}
	index, err := a.cachedLibraryIndex(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	key := r.URL.Query().Encode()
	a.libraryMu.Lock()
	previous := a.libraryPages[key]
	a.libraryMu.Unlock()
	current := previous
	if current == nil || current.page.Revision != index.revision {
		page := index.browse(query)
		raw, err := json.Marshal(page)
		if err != nil {
			apiError(w, 500, err)
			return
		}
		current = &libraryPageCache{page: page, raw: raw, etag: `W/"` + digest(string(raw)) + `"`}
		a.libraryMu.Lock()
		if a.libraryPages == nil {
			a.libraryPages = map[string]*libraryPageCache{}
		}
		if len(a.libraryPages) >= 32 && a.libraryPages[key] == nil {
			for old := range a.libraryPages {
				delete(a.libraryPages, old)
				break
			}
		}
		if a.libraryIndex == index {
			a.libraryPages[key] = current
		}
		a.libraryMu.Unlock()
	}
	raw := current.raw
	w.Header().Add("Vary", "X-Library-Delta")
	if r.Header.Get("X-Library-Delta") == "1" && previous != nil && previous != current && r.Header.Get("If-None-Match") == previous.etag {
		delta := current.page.delta(previous.page, previous.etag)
		if encoded, err := json.Marshal(delta); err == nil && len(encoded) < len(raw) {
			raw = encoded
		}
	}
	serveLibraryJSON(w, r, raw, current.etag)
}
