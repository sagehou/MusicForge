package forge

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
)

var libraryPriority = []string{"failed", "possibly_damaged", "expired", "missing", "changed", "needs_rebuild", "unverified", "ready"}

type libraryQuery struct {
	View, Artist, Album, Search, Status, Lyrics, Script, Sort, Locale string
	HasAlbum                                                          bool
	Offset                                                            int
	Anchor                                                            int64
}

func parseLibraryQuery(values url.Values) (libraryQuery, error) {
	q := libraryQuery{View: values.Get("view"), Artist: values.Get("artist"), Album: values.Get("album"), HasAlbum: values.Has("album"), Search: strings.ToLower(strings.TrimSpace(values.Get("q"))), Status: values.Get("status"), Lyrics: values.Get("lyrics"), Script: values.Get("lyrics_script"), Sort: values.Get("sort"), Locale: values.Get("locale")}
	invalid := errors.New("Invalid library filters or page")
	if len(values.Encode()) > 4096 || len(q.Search) > 1024 || !slices.Contains([]string{"", "tracks", "albums", "artists"}, q.View) || !slices.Contains(append([]string{"", "all"}, libraryPriority...), q.Status) || !slices.Contains([]string{"", "all", "ready", "unchecked", "not_found", "instrumental", "needs_review", "failed"}, q.Lyrics) || !slices.Contains([]string{"", "all", "traditional", "simplified", "unknown", "unidentified"}, q.Script) || !slices.Contains([]string{"", "name", "attention"}, q.Sort) {
		return q, invalid
	}
	if q.View == "" {
		q.View = "albums"
	}
	if raw := values.Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return q, invalid
		}
		q.Offset = value
	}
	if raw := values.Get("anchor"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return q, invalid
		}
		q.Anchor = value
	}
	return q, nil
}

type libraryIdentity struct {
	ArtistKey string `json:"artistKey"`
	AlbumKey  string `json:"albumKey"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
}

func sourceIdentity(source Source, locale string) libraryIdentity {
	parts := strings.Split(source.Rel, "/")
	artist := source.Metadata["albumartist"]
	if artist == "" {
		artist = source.Metadata["album_artist"]
	}
	if artist == "" {
		artist = source.Artist
	}
	if artist == "" && len(parts) > 2 {
		artist = parts[0]
	}
	if artist == "" {
		artist = "Unknown artist"
		if locale == "zh-CN" {
			artist = "未知艺人"
		}
	}
	album := source.Album
	if album == "" && len(parts) > 1 {
		album = parts[len(parts)-2]
	}
	if album == "" {
		album = "Unknown album"
		if locale == "zh-CN" {
			album = "未知专辑"
		}
	}
	identity := libraryIdentity{ArtistKey: "tag:" + artist, AlbumKey: strings.Join(parts[:len(parts)-1], "/"), Artist: artist, Album: album}
	if len(parts) > 2 {
		identity.ArtistKey, identity.AlbumKey = parts[0], strings.Join(parts[:2], "/")
	}
	return identity
}

func (q libraryQuery) inScope(identity libraryIdentity) bool {
	return (q.Artist == "" || q.Artist == identity.ArtistKey) && (!q.HasAlbum || q.Album == identity.AlbumKey)
}

func sourceLyricsState(source Source) string {
	if source.Output == "" || !source.OutputPresent {
		return ""
	}
	if source.Lyrics == nil {
		return "unchecked"
	}
	if source.Lyrics.State == "ready" && source.Lyrics.Instrumental {
		return "instrumental"
	}
	return source.Lyrics.State
}

func (q libraryQuery) matches(source Source, identity libraryIdentity) bool {
	if !q.inScope(identity) {
		return false
	}
	if q.Anchor == source.ID {
		return true
	}
	if q.Status != "" && q.Status != "all" && (q.Status != "possibly_damaged" && source.Status != q.Status || q.Status == "possibly_damaged" && !source.PossiblyDamaged) {
		return false
	}
	if q.Lyrics != "" && q.Lyrics != "all" && sourceLyricsState(source) != q.Lyrics {
		return false
	}
	script := source.LyricsScript
	if script == "" {
		script = "unidentified"
	}
	if q.Script != "" && q.Script != "all" && (source.Output == "" || !source.OutputPresent || script != q.Script) {
		return false
	}
	return q.Search == "" || strings.Contains(strings.ToLower(identity.Artist+" "+source.Artist+" "+identity.Album+" "+source.Title+" "+source.Rel), q.Search)
}

func sourceRank(source Source) int {
	if rank := slices.Index(libraryPriority, source.Status); rank >= 0 {
		return rank
	}
	return len(libraryPriority)
}

// Numeric runs keep Track 2 before Track 10 without a locale-dependent database
// collation or any change to the persisted schema.
func libraryNameCompare(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for len(a) > 0 && len(b) > 0 {
		if a[0] >= '0' && a[0] <= '9' && b[0] >= '0' && b[0] <= '9' {
			i, j := 0, 0
			for i < len(a) && a[i] >= '0' && a[i] <= '9' {
				i++
			}
			for j < len(b) && b[j] >= '0' && b[j] <= '9' {
				j++
			}
			x, y := strings.TrimLeft(a[:i], "0"), strings.TrimLeft(b[:j], "0")
			if len(x) != len(y) {
				if len(x) < len(y) {
					return -1
				}
				return 1
			}
			if order := strings.Compare(x, y); order != 0 {
				return order
			}
			a, b = a[i:], b[j:]
			continue
		}
		if a[0] != b[0] {
			if a[0] < b[0] {
				return -1
			}
			return 1
		}
		a, b = a[1:], b[1:]
	}
	return strings.Compare(a, b)
}

func (index *libraryIndex) matching(q libraryQuery) []int {
	rows := []int{}
	for i, source := range index.sources {
		if q.matches(source, sourceIdentity(source, q.Locale)) {
			rows = append(rows, i)
		}
	}
	slices.SortStableFunc(rows, func(i, j int) int {
		a, b := index.sources[i], index.sources[j]
		if q.HasAlbum {
			if delta := max(1, a.Disc) - max(1, b.Disc); delta != 0 {
				return delta
			}
			if delta := a.Track - b.Track; delta != 0 {
				return delta
			}
			return libraryNameCompare(a.Rel, b.Rel)
		}
		if q.Sort == "attention" {
			if delta := sourceRank(a) - sourceRank(b); delta != 0 {
				return delta
			}
		}
		aName, bName := a.Title, b.Title
		if aName == "" {
			aName = a.Rel
		}
		if bName == "" {
			bName = b.Rel
		}
		if order := libraryNameCompare(aName, bName); order != 0 {
			return order
		}
		return libraryNameCompare(a.Rel, b.Rel)
	})
	return rows
}

type libraryCover struct {
	URL string `json:"artwork_url"`
}
type libraryGroup struct {
	libraryIdentity
	Key           string         `json:"key"`
	Name          string         `json:"name"`
	IDs           []int64        `json:"ids,omitempty"`
	Count         int            `json:"count"`
	Albums        int            `json:"albums"`
	Rank          int            `json:"rank"`
	Ready         int            `json:"ready"`
	Damaged       bool           `json:"damaged"`
	Covers        []libraryCover `json:"covers"`
	Duration      float64        `json:"duration"`
	DurationKnown bool           `json:"duration_known"`
	Audio         []*audioFormat `json:"audio"`
	AudioCount    int            `json:"audio_count"`
	Playable      int            `json:"playable"`
	OutputFormats []string       `json:"output_formats"`
	OutputSize    int64          `json:"output_size"`
	OutputKnown   bool           `json:"output_known"`
	LyricsReady   int            `json:"lyrics_ready"`
	Discs         []int          `json:"discs"`
	Formats       []string       `json:"formats"`
	albums        map[string]bool
	audio         map[string]bool
	covers        map[string]bool
}

func newLibraryGroup(identity libraryIdentity, artists bool) *libraryGroup {
	key, name := identity.ArtistKey+"\x00"+identity.AlbumKey, identity.Album
	if artists {
		key, name = identity.ArtistKey, identity.Artist
	}
	return &libraryGroup{libraryIdentity: identity, Key: key, Name: name, Rank: len(libraryPriority), DurationKnown: true, OutputKnown: true, IDs: []int64{}, Covers: []libraryCover{}, Audio: []*audioFormat{}, OutputFormats: []string{}, Discs: []int{}, Formats: []string{}, albums: map[string]bool{}, audio: map[string]bool{}, covers: map[string]bool{}}
}

func (group *libraryGroup) add(source Source, identity libraryIdentity, includeID bool) {
	group.Count++
	if includeID {
		group.IDs = append(group.IDs, source.ID)
	}
	group.albums[identity.AlbumKey] = true
	group.Albums = len(group.albums)
	group.Rank = min(group.Rank, sourceRank(source))
	if source.Status == "ready" {
		group.Ready++
	}
	group.Damaged = group.Damaged || source.PossiblyDamaged
	group.Duration += source.Duration
	group.DurationKnown = group.DurationKnown && source.Duration > 0
	if source.ArtworkURL != "" && len(group.Covers) < 4 {
		_, signature, found := strings.Cut(source.ArtworkURL, "?v=")
		if !found {
			signature = source.ArtworkURL
		}
		if !group.covers[signature] {
			group.Covers = append(group.Covers, libraryCover{source.ArtworkURL})
			group.covers[signature] = true
		}
	}
	format, _ := json.Marshal(source.SourceAudio)
	if !group.audio[string(format)] {
		group.audio[string(format)] = true
		group.AudioCount++
		if len(group.Audio) < 3 {
			group.Audio = append(group.Audio, source.SourceAudio)
		}
	}
	disc := max(1, source.Disc)
	if !slices.Contains(group.Discs, disc) {
		group.Discs = append(group.Discs, disc)
	}
	ext := strings.ToUpper(strings.TrimPrefix(path.Ext(source.Rel), "."))
	if !slices.Contains(group.Formats, ext) {
		group.Formats = append(group.Formats, ext)
	}
	if source.Output != "" && source.OutputPresent {
		group.Playable++
		if source.Lyrics != nil && source.Lyrics.State == "ready" {
			group.LyricsReady++
		}
		if source.OutputSize == nil {
			group.OutputKnown = false
		} else {
			group.OutputSize += *source.OutputSize
		}
		ext := strings.ToUpper(strings.TrimPrefix(path.Ext(source.Output), "."))
		if !slices.Contains(group.OutputFormats, ext) {
			group.OutputFormats = append(group.OutputFormats, ext)
		}
	}
}

type libraryCounts struct {
	Tracks    int `json:"tracks"`
	Artists   int `json:"artists"`
	Albums    int `json:"albums"`
	Expired   int `json:"expired"`
	Removable int `json:"removable"`
	Present   int `json:"present"`
	Deleted   int `json:"deleted"`
	Outputs   int `json:"outputs"`
	Failed    int `json:"failed"`
}
type libraryLyricsInventory struct {
	Counts  map[string]int `json:"counts"`
	Total   int            `json:"total"`
	Checked int            `json:"checked"`
}
type libraryPage struct {
	Revision      string                 `json:"revision"`
	Rows          []Source               `json:"rows"`
	Groups        []*libraryGroup        `json:"groups"`
	Total         int                    `json:"total"`
	Matched       int                    `json:"matched"`
	Offset        int                    `json:"offset"`
	Counts        libraryCounts          `json:"counts"`
	Scope         *libraryGroup          `json:"scope"`
	Lyrics        libraryLyricsInventory `json:"lyrics"`
	Attention     int                    `json:"attention"`
	Active        *Source                `json:"active,omitempty"`
	ContextSource *Source                `json:"context_source,omitempty"`
	Previous      int64                  `json:"previous,omitempty"`
	Next          int64                  `json:"next,omitempty"`
}

func (index *libraryIndex) browse(q libraryQuery) libraryPage {
	page := libraryPage{Revision: index.revision, Rows: []Source{}, Groups: []*libraryGroup{}, Scope: newLibraryGroup(libraryIdentity{}, false), Lyrics: libraryLyricsInventory{Counts: map[string]int{"ready": 0, "unchecked": 0, "not_found": 0, "instrumental": 0, "needs_review": 0, "failed": 0}}}
	artists, albums := map[string]bool{}, map[string]bool{}
	scoped := []Source{}
	for _, source := range index.sources {
		identity := sourceIdentity(source, q.Locale)
		artists[identity.ArtistKey] = true
		albums[identity.ArtistKey+"\x00"+identity.AlbumKey] = true
		page.Counts.Tracks++
		if source.Present {
			page.Counts.Present++
			if source.Error != "" {
				page.Counts.Failed++
			}
		} else {
			page.Counts.Deleted++
			if source.Output != "" {
				page.Counts.Expired++
			} else if !source.OutputPresent {
				page.Counts.Removable++
			}
		}
		if source.OutputPresent {
			page.Counts.Outputs++
		}
		if q.Anchor == source.ID {
			// A cached page must not retain the full index backing array.
			active := source
			page.Active = &active
		}
		if !q.inScope(identity) {
			continue
		}
		if page.ContextSource == nil {
			contextSource := source
			page.ContextSource = &contextSource
			page.Scope = newLibraryGroup(identity, q.Artist != "" && !q.HasAlbum)
		}
		page.Scope.add(source, identity, false)
		scoped = append(scoped, source)
		if state := sourceLyricsState(source); state != "" {
			page.Lyrics.Counts[state]++
			page.Lyrics.Total++
		}
	}
	page.Counts.Artists, page.Counts.Albums = len(artists), len(albums)
	page.Lyrics.Checked = page.Lyrics.Total - page.Lyrics.Counts["unchecked"]
	page.Attention = libraryAttention(scoped)
	rows := index.matching(q)
	page.Matched = len(rows)
	tracks := q.HasAlbum || q.View == "tracks"
	if tracks {
		page.Total = len(rows)
		for position, i := range rows {
			if index.sources[i].ID == q.Anchor {
				q.Offset = position / 50 * 50
				if position > 0 {
					page.Previous = index.sources[rows[position-1]].ID
				}
				if position+1 < len(rows) {
					page.Next = index.sources[rows[position+1]].ID
				}
				break
			}
		}
	} else {
		grouped := map[string]*libraryGroup{}
		artists := q.View == "artists" && q.Artist == ""
		for _, i := range rows {
			source := index.sources[i]
			identity := sourceIdentity(source, q.Locale)
			key := identity.ArtistKey + "\x00" + identity.AlbumKey
			if artists {
				key = identity.ArtistKey
			}
			group := grouped[key]
			if group == nil {
				group = newLibraryGroup(identity, artists)
				grouped[key] = group
				page.Groups = append(page.Groups, group)
			}
			group.add(source, identity, true)
		}
		slices.SortStableFunc(page.Groups, func(a, b *libraryGroup) int {
			if q.Sort == "attention" && a.Rank != b.Rank {
				return a.Rank - b.Rank
			}
			if result := libraryNameCompare(a.Name, b.Name); result != 0 {
				return result
			}
			if result := libraryNameCompare(a.Artist, b.Artist); result != 0 {
				return result
			}
			return strings.Compare(a.Key, b.Key)
		})
		page.Total = len(page.Groups)
	}
	page.Offset = min(q.Offset, max(0, (page.Total-1)/50*50))
	end := min(page.Offset+50, page.Total)
	if tracks {
		for _, i := range rows[page.Offset:end] {
			page.Rows = append(page.Rows, index.sources[i])
		}
	} else {
		page.Groups = append([]*libraryGroup{}, page.Groups[page.Offset:end]...)
	}
	page.Scope.albums, page.Scope.audio, page.Scope.covers = nil, nil, nil
	for _, group := range page.Groups {
		group.albums, group.audio, group.covers = nil, nil, nil
	}
	return page
}

// Match the existing quality summary without reading files or transferring the
// full scope. Detailed maintenance data is fetched only when its panel opens.
func libraryAttention(sources []Source) int {
	attention := map[int64]bool{}
	hashes, discs := map[string][]Source{}, map[string][]Source{}
	for _, source := range sources {
		if !source.Present && !source.OutputPresent {
			continue
		}
		if strings.TrimSpace(source.Artist) == "" || strings.TrimSpace(source.Album) == "" || strings.TrimSpace(source.Title) == "" || source.Duration < 10 || source.Duration > 3600 || source.Status == "failed" || source.Error != "" || source.PossiblyDamaged || source.ArtworkURL == "" || source.OutputPresent && (source.Lyrics == nil || source.Lyrics.State != "ready" && source.Lyrics.State != "instrumental") {
			attention[source.ID] = true
		}
		if source.Present && source.Hash != "" {
			hashes[source.Hash] = append(hashes[source.Hash], source)
		}
		parts := strings.Split(source.Rel, "/")
		directory := strings.Join(parts[:len(parts)-1], "/")
		if len(parts) >= 3 {
			directory = strings.Join(parts[:2], "/")
		}
		key := directory + "\x00" + strconv.Itoa(max(1, source.Disc))
		discs[key] = append(discs[key], source)
	}
	for _, group := range hashes {
		if len(group) > 1 {
			for _, source := range group {
				attention[source.ID] = true
			}
		}
	}
	for _, group := range discs {
		numbers, largest := map[int]bool{}, 0
		for _, source := range group {
			if source.Track > 0 {
				numbers[source.Track] = true
				largest = max(largest, source.Track)
			}
		}
		if len(numbers) != len(group) || largest != len(numbers) {
			for _, source := range group {
				attention[source.ID] = true
			}
		}
	}
	return len(attention)
}

type libraryDelta struct {
	libraryPage
	Base       string   `json:"base"`
	RowOrder   []int64  `json:"row_order"`
	GroupOrder []string `json:"group_order"`
}

func (page libraryPage) delta(previous libraryPage, etag string) libraryDelta {
	result := libraryDelta{libraryPage: page, Base: etag, RowOrder: []int64{}, GroupOrder: []string{}}
	result.Rows, result.Groups = []Source{}, []*libraryGroup{}
	oldRows, oldGroups := map[int64]string{}, map[string]string{}
	for _, row := range previous.Rows {
		raw, _ := json.Marshal(row)
		oldRows[row.ID] = string(raw)
	}
	for _, group := range previous.Groups {
		raw, _ := json.Marshal(group)
		oldGroups[group.Key] = string(raw)
	}
	for _, row := range page.Rows {
		result.RowOrder = append(result.RowOrder, row.ID)
		raw, _ := json.Marshal(row)
		if oldRows[row.ID] != string(raw) {
			result.Rows = append(result.Rows, row)
		}
	}
	for _, group := range page.Groups {
		result.GroupOrder = append(result.GroupOrder, group.Key)
		raw, _ := json.Marshal(group)
		if oldGroups[group.Key] != string(raw) {
			result.Groups = append(result.Groups, group)
		}
	}
	return result
}

func (a *App) libraryScope(w http.ResponseWriter, r *http.Request) {
	q, err := parseLibraryQuery(r.URL.Query())
	if err != nil {
		apiError(w, 400, err)
		return
	}
	index, err := a.cachedLibraryIndex(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	rows := []Source{}
	for _, source := range index.sources {
		if q.inScope(sourceIdentity(source, q.Locale)) {
			rows = append(rows, source)
		}
	}
	respondLibrary(w, r, rows)
}

func (a *App) librarySelection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query    string   `json:"query"`
		IDs      *[]int64 `json:"ids"`
		IDsOnly  bool     `json:"ids_only"`
		Playable bool     `json:"playable"`
	}
	if !decode(w, r, &body) {
		return
	}
	values, err := url.ParseQuery(body.Query)
	if err != nil {
		apiError(w, 400, errors.New("Invalid library filters or page"))
		return
	}
	q, err := parseLibraryQuery(values)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	index, err := a.cachedLibraryIndex(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	positions := []int{}
	if body.IDs != nil {
		if len(*body.IDs) > 10000 {
			apiError(w, 400, errors.New("Select at most 10000 tracks at a time"))
			return
		}
		seen := map[int64]bool{}
		for _, id := range *body.IDs {
			if id <= 0 {
				apiError(w, 400, errors.New("Library record IDs must be positive"))
				return
			}
			if i, ok := index.byID[id]; ok && !seen[id] {
				positions = append(positions, i)
				seen[id] = true
			}
		}
	} else {
		positions = index.matching(q)
	}
	rows, ids := []Source{}, []int64{}
	for _, i := range positions {
		source := index.sources[i]
		if body.Playable && (source.Output == "" || !source.OutputPresent) {
			continue
		}
		ids = append(ids, source.ID)
		if !body.IDsOnly {
			rows = append(rows, source)
		}
	}
	if body.IDsOnly {
		respond(w, 200, map[string]any{"ids": ids})
		return
	}
	if len(rows) > 10000 {
		apiError(w, 400, errors.New("Select at most 10000 tracks at a time"))
		return
	}
	respond(w, 200, rows)
}
