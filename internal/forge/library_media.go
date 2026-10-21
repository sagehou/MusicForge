package forge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// Optional display data stays outside source tags, ownership and recovery formats.
// Older schema-2 applications can ignore it; snapshot mismatches hide stale data.
type sourceMedia struct {
	Version int          `json:"version"`
	Path    string       `json:"path"`
	Hash    string       `json:"hash"`
	Size    int64        `json:"size"`
	Mtime   int64        `json:"mtime"`
	Bitrate int64        `json:"bitrate"` // bits per second, as reported by ffprobe
	Audio   *audioFormat `json:"audio,omitempty"`
}

type audioFormat struct {
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"` // Hz
	BitDepth   int    `json:"bit_depth,omitempty"`
	Channels   int    `json:"channels,omitempty"`
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
	return a.cacheSourceMedia(source, p.bitrate(), p.audioFormat())
}

func (p probeData) audioFormat() *audioFormat {
	for _, stream := range p.Streams {
		if stream.Type != "audio" {
			continue
		}
		rate, _ := strconv.Atoi(stream.SampleRate)
		bits, _ := strconv.Atoi(stream.RawBits)
		if bits <= 0 {
			bits = stream.SampleBits
		}
		return &audioFormat{Codec: stream.Codec, SampleRate: max(0, rate), BitDepth: max(0, bits), Channels: max(0, stream.Channels)}
	}
	return &audioFormat{}
}

func (a *App) cacheSourceMedia(source Source, bitrate int64, audio *audioFormat) error {
	data, _ := json.Marshal(sourceMedia{Version: 1, Path: source.Rel, Hash: source.Hash, Size: source.Size, Mtime: source.Mtime, Bitrate: bitrate, Audio: audio})
	return a.setMeta("source-media:"+strconv.FormatInt(source.ID, 10), string(data))
}

// Backfill display fields separately from tag indexing, hashing and encoding.
func (a *App) backfillSourceMedia(ctx context.Context, root string, source Source) error {
	var audio *audioFormat
	bitrate := source.SourceBitrate
	if strings.EqualFold(filepath.Ext(source.Rel), ".flac") {
		err := a.sourceIO(ctx, "flac-info", root, source.Rel, func(e sourceEvent) error {
			if e.Info != nil && !sameStat(*e.Info, source) {
				return errors.New("source changed while reading audio specification")
			}
			if e.Audio != nil {
				audio = e.Audio
			}
			return nil
		})
		if err != nil {
			return err
		}
		// STREAMINFO has no compressed bitrate. Preserve a measured value;
		// never replace it with the uncompressed PCM rate or an estimate.
	} else {
		before, err := a.sourceStat(ctx, root, source.Rel)
		if err != nil {
			return err
		}
		if !sameStat(before, source) {
			return errors.New("source changed before reading audio specification")
		}
		p, err := a.probeEntries(ctx, before.Path, "stream=codec_name,codec_type,bit_rate,sample_rate,bits_per_raw_sample,bits_per_sample,channels:format=bit_rate")
		if err != nil {
			return err
		}
		after, err := a.sourceStat(ctx, root, source.Rel)
		if err != nil {
			return err
		}
		if !sameStat(after, source) {
			return errors.New("source changed while reading audio specification")
		}
		bitrate, audio = p.bitrate(), p.audioFormat()
	}
	if audio == nil {
		return errors.New("missing source audio specification")
	}
	return a.cacheSourceMedia(source, bitrate, audio)
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
				source.SourceBitrate, source.SourceAudio = media.Bitrate, media.Audio
				source.mediaInfoKnown = media.Audio != nil
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
