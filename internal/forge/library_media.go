package forge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Optional display data stays outside source tags, ownership and recovery formats.
// Older schema-2 applications can ignore it; snapshot mismatches hide stale data.
type sourceMedia struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"`
	Bitrate int64  `json:"bitrate"` // bits per second, as reported by ffprobe
}

type audioSize struct {
	Version   int    `json:"version"`
	Path      string `json:"path"`
	Signature string `json:"signature"`
	Size      int64  `json:"size"`
}

func (p probeData) bitrate() int64 {
	for _, stream := range p.Streams {
		if stream.Type == "audio" {
			if rate, err := strconv.ParseInt(stream.Bitrate, 10, 64); err == nil && rate > 0 {
				return rate
			}
			break
		}
	}
	if rate, err := strconv.ParseInt(p.Format.Bitrate, 10, 64); err == nil && rate > 0 {
		return rate
	}
	return 0
}

func (a *App) rememberSourceMedia(source Source, p probeData) error {
	data, _ := json.Marshal(sourceMedia{1, source.Rel, source.Hash, source.Size, source.Mtime, p.bitrate()})
	return a.setMeta("source-media:"+strconv.FormatInt(source.ID, 10), string(data))
}

func (a *App) rememberAudioSize(id int64, path string, size int64) error {
	_, err := a.db.Exec(`INSERT INTO meta(key,value)
	 SELECT 'audio-size:'||source_id,json_object('version',1,'path',path,'signature',signature,'size',?)
	 FROM managed WHERE source_id=? AND path=? AND kind='audio' AND signature<>''
	 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, size, id, path)
	return err
}

// Browsing uses one database read, never a probe or stat of either music mount.
func (a *App) annotateMedia(ctx context.Context, sources []Source) error {
	if len(sources) == 0 {
		return nil
	}
	byID := make(map[int64]*Source, len(sources))
	for i := range sources {
		byID[sources[i].ID] = &sources[i]
	}
	// Indexed prefix reads start with existing annotations, avoiding another walk
	// of the source table (especially for large older libraries without caches).
	rows, err := a.reads.QueryContext(ctx, `SELECT key,value,'' FROM meta WHERE key GLOB 'source-media:*'
	 UNION ALL
	 SELECT z.key,z.value,m.signature FROM meta z JOIN managed m
	 ON m.path=json_extract(CASE WHEN json_valid(z.value) THEN z.value ELSE '{}' END,'$.path')
	 AND z.key='audio-size:'||m.source_id AND m.kind='audio'
	 WHERE z.key GLOB 'audio-size:*'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw, signature string
		if err = rows.Scan(&key, &raw, &signature); err != nil {
			return err
		}
		kind, identifier, _ := strings.Cut(key, ":")
		id, _ := strconv.ParseInt(identifier, 10, 64)
		source := byID[id]
		if source == nil {
			continue
		}
		if kind == "source-media" {
			var media sourceMedia
			if json.Unmarshal([]byte(raw), &media) == nil && media.Version == 1 && media.Path == source.Rel && media.Hash == source.Hash && media.Size == source.Size && media.Mtime == source.Mtime && media.Bitrate >= 0 {
				source.SourceBitrate, source.mediaInfoKnown = media.Bitrate, true
			}
		} else {
			var audio audioSize
			if json.Unmarshal([]byte(raw), &audio) == nil && audio.Version == 1 && audio.Path == source.Output && audio.Signature == signature && signature != "" && audio.Size >= 0 && source.OutputPresent {
				source.OutputSize = &audio.Size
			}
		}
	}
	return rows.Err()
}
