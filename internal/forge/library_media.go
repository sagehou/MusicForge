package forge

import (
	"context"
	"encoding/json"
	"strconv"
)

// Optional display data stays outside source tags, ownership and recovery formats.
// Older schema-2 applications can ignore it; snapshot mismatches hide stale data.
type sourceMedia struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"`
	Bitrate int64  `json:"bitrate"` // bits per second, as reported by ffprobe
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
	data, _ := json.Marshal(sourceMedia{1, source.Rel, source.Size, source.Mtime, p.bitrate()})
	return a.setMeta("source-media:"+strconv.FormatInt(source.ID, 10), string(data))
}

func (a *App) rememberAudioSize(id int64, path string, size int64) error {
	_, err := a.db.Exec(`INSERT INTO meta(key,value)
	 SELECT 'audio-size:'||signature,? FROM managed WHERE source_id=? AND path=? AND kind='audio' AND signature<>''
	 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatInt(size, 10), id, path)
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
	rows, err := a.reads.QueryContext(ctx, `SELECT s.id,coalesce(b.value,''),coalesce(z.value,'') FROM sources s
	 LEFT JOIN meta b ON b.key='source-media:'||s.id
	 LEFT JOIN managed m ON m.source_id=s.id AND m.path=s.output AND m.kind='audio'
	 LEFT JOIN meta z ON z.key='audio-size:'||m.signature`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw, size string
		if err = rows.Scan(&id, &raw, &size); err != nil {
			return err
		}
		source := byID[id]
		if source == nil {
			continue
		}
		var media sourceMedia
		if json.Unmarshal([]byte(raw), &media) == nil && media.Version == 1 && media.Path == source.Rel && media.Size == source.Size && media.Mtime == source.Mtime && media.Bitrate >= 0 {
			source.SourceBitrate, source.mediaInfoKnown = media.Bitrate, true
		}
		if bytes, err := strconv.ParseInt(size, 10, 64); err == nil && bytes >= 0 && source.OutputPresent {
			source.OutputSize = &bytes
		}
	}
	return rows.Err()
}
