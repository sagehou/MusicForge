package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLibraryBrowseLargeIndex(t *testing.T) {
	index := &libraryIndex{revision: "fixture", byID: map[int64]int{}}
	for i := 0; i < 5000; i++ {
		source := Source{ID: int64(i + 1), Rel: fmt.Sprintf("Artist %d/Album %d/%d.flac", i/100, i/10, i%10+1), Title: fmt.Sprintf("Track %04d", i+1), Artist: fmt.Sprintf("Artist %d", i/100), Album: fmt.Sprintf("Album %d", i/10), Track: i%10 + 1, Disc: 1, Duration: 180, Present: true, Status: "ready", Output: fmt.Sprintf("%d.opus", i), OutputPresent: true, Metadata: map[string]string{}}
		index.byID[source.ID] = i
		index.sources = append(index.sources, source)
	}
	query := func(raw string) libraryQuery {
		t.Helper()
		values, _ := url.ParseQuery(raw)
		q, err := parseLibraryQuery(values)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	page := index.browse(query("view=tracks&offset=50"))
	if len(page.Rows) != 50 || page.Rows[0].ID != 51 || page.Total != 5000 || page.Counts.Artists != 50 || page.Counts.Albums != 500 || page.Lyrics.Total != 5000 {
		t.Fatal("incorrect large-library page", page.Counts, page.Total, len(page.Rows))
	}
	full, _ := json.Marshal(index.sources)
	paged, _ := json.Marshal(page)
	if len(paged) >= len(full)/20 {
		t.Fatal("paged response scales with full library", len(paged), len(full))
	}
	t.Logf("5000 tracks: full JSON %d bytes, 50-track page %d bytes", len(full), len(paged))
	albums := index.browse(query("view=albums"))
	if len(albums.Groups) != 50 || albums.Total != 500 || albums.Groups[2].Name != "Album 2" || len(albums.Rows) != 0 || albums.Groups[0].Count != 10 {
		t.Fatal("album grouping or natural sort", albums.Total)
	}
	scope := index.browse(query("artist=Artist+0&album=Artist+0/Album+0&q=0005"))
	if scope.Total != 1 || scope.Rows[0].ID != 5 || scope.Scope.Count != 10 || scope.Lyrics.Total != 10 {
		t.Fatal("search changed maintenance scope", scope.Total, scope.Scope.Count)
	}
	anchored := index.browse(query("view=tracks&anchor=51&offset=0"))
	if anchored.Offset != 50 || anchored.Previous != 50 || anchored.Next != 52 || anchored.Active.ID != 51 {
		t.Fatal("anchor lost page or neighbors", anchored.Offset, anchored.Previous, anchored.Next)
	}
	index.sources[50].Lyrics = &LyricsStatus{State: "ready", Instrumental: true}
	if got := index.browse(query("view=tracks&lyrics=instrumental")); got.Total != 1 || got.Rows[0].ID != 51 {
		t.Fatal("instrumental placeholder filter", got.Total)
	}
	index.sources[49].Status, index.sources[49].Error = "failed", "fixture"
	if got := index.browse(query("view=tracks&status=failed&anchor=51")); got.Total != 2 || got.Previous != 50 {
		t.Fatal("expanded track disappeared after filtering", got.Total)
	}
	index.sources = slices.Delete(index.sources, 60, 61)
	changed := index.browse(query("view=tracks&offset=50"))
	delta := changed.delta(page, "old")
	if len(delta.Rows) != 2 || len(delta.RowOrder) != 50 || slices.Contains(delta.RowOrder, int64(61)) || delta.Rows[0].ID != 51 || delta.Rows[1].ID != 101 {
		t.Fatal("delta did not describe changes/removal/page fill", delta.RowOrder, delta.Rows)
	}
	for _, raw := range []string{"offset=-1", "offset=oops", "anchor=0", "lyrics=wrong", "status=wrong", "lyrics_script=wrong", "sort=wrong", "view=wrong"} {
		values, _ := url.ParseQuery(raw)
		if _, err := parseLibraryQuery(values); err == nil {
			t.Fatal("accepted invalid query", raw)
		}
	}
}

func TestLibraryIndexInvalidationSelectionAndRestart(t *testing.T) {
	a, settings := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 120; i++ {
		if _, err = tx.Exec("INSERT INTO sources(id,rel,title,artist,album,duration) VALUES(?,?,?,?,?,180)", i, fmt.Sprintf("Artist/Album/%03d.flac", i), fmt.Sprintf("Track %03d", i), "Artist", "Album"); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Browsing and selection remain available with both music mounts absent.
	for _, path := range []string{settings.Source, settings.Output} {
		if err = os.Rename(path, path+"-offline"); err != nil {
			t.Fatal(err)
		}
	}
	read := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/library/browse?view=tracks", nil)
		req.Header.Set("If-None-Match", etag)
		req.Header.Set("X-Library-Delta", "1")
		w := httptest.NewRecorder()
		a.browseLibrary(w, req)
		if w.Code != 200 && w.Code != 304 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w
	}
	first := read("")
	tag := first.Header().Get("ETag")
	index, err := a.cachedLibraryIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if read(tag).Code != 304 {
		t.Fatal("unchanged page sent again")
	}
	cached, err := a.cachedLibraryIndex(context.Background())
	if err != nil || cached != index {
		t.Fatal("unchanged index rebuilt", err)
	}
	writer, err := sql.Open("sqlite", filepath.Join(a.cfg.ConfigDir, "musicforge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.Exec("UPDATE sources SET error='changed by compatible writer' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	changed := read(tag)
	var delta libraryDelta
	if err = json.Unmarshal(changed.Body.Bytes(), &delta); err != nil || delta.Base != tag || len(delta.Rows) != 1 || delta.Rows[0].ID != 2 || delta.Rows[0].Status != "failed" {
		t.Fatal("external write hidden or missing delta", err, changed.Body.String())
	}
	if read(changed.Header().Get("ETag")).Code != 304 {
		t.Fatal("new page not cached")
	}
	selection := httptest.NewRecorder()
	a.librarySelection(selection, httptest.NewRequest("POST", "/api/library/selection", strings.NewReader(`{"ids":[120,1,120,999]}`)))
	var selected []Source
	if err = json.Unmarshal(selection.Body.Bytes(), &selected); err != nil || len(selected) != 2 || selected[0].ID != 120 || selected[1].ID != 1 {
		t.Fatal("cross-page selection", err, selection.Body.String())
	}
	ordered := httptest.NewRecorder()
	a.librarySelection(ordered, httptest.NewRequest("POST", "/api/library/selection", strings.NewReader(`{"query":"view=tracks","ids_only":true}`)))
	var ids struct {
		IDs []int64 `json:"ids"`
	}
	if err = json.Unmarshal(ordered.Body.Bytes(), &ids); err != nil || len(ids.IDs) != 120 || ids.IDs[119] != 120 {
		t.Fatal("ordered review scope", err)
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	a, err = New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	restarted := read(changed.Header().Get("ETag"))
	var page libraryPage
	if restarted.Code != 200 || json.Unmarshal(restarted.Body.Bytes(), &page) != nil || len(page.Rows) != 50 || page.Rows[1].Status != "failed" || strings.Contains(restarted.Body.String(), `"base":`) {
		t.Fatal("restart must return a complete page")
	}
}
