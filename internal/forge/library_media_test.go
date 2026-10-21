package forge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLibraryMediaBackfillsWithoutRebuildingAndRejectsStaleSnapshots(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "Album/01.flac", "Track", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	read := func() Source {
		t.Helper()
		list, err := a.sourcesWithStatus()
		if err != nil || len(list) != 1 {
			t.Fatal("library media snapshot", list, err)
		}
		return list[0]
	}
	original := read()
	info, err := os.Stat(filepath.Join(s.Output, original.Output))
	if err != nil {
		t.Fatal(err)
	}
	if original.SourceBitrate <= 0 || original.OutputSize == nil || *original.OutputSize != info.Size() {
		t.Fatal("missing measured source bitrate or actual output size", original)
	}
	if original.SourceAudio == nil || original.SourceAudio.Codec != "flac" || original.SourceAudio.BitDepth != 16 || original.SourceAudio.SampleRate != 44100 || original.SourceAudio.Channels != 1 {
		t.Fatal("missing FLAC specification in the original probe", original.SourceAudio)
	}
	// A v0.2.26 index has a measured bitrate but no audio specification.
	if _, err = a.db.Exec("UPDATE meta SET value=json_remove(value,'$.audio') WHERE key GLOB 'source-media:*'"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("DELETE FROM meta WHERE key GLOB 'audio-size:*'"); err != nil {
		t.Fatal(err)
	}
	if old := read(); old.SourceBitrate != original.SourceBitrate || old.SourceAudio != nil || old.mediaInfoKnown || old.OutputSize != nil {
		t.Fatal("legacy measurements lost or missing specification guessed", old)
	}
	var before, after int
	if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind IN ('convert','move')").Scan(&before); err != nil {
		t.Fatal(err)
	}
	a.cfg.FFprobe = "/nonexistent/ffprobe"
	trace := filepath.Join(t.TempDir(), "source-operations")
	t.Setenv("MUSICFORGE_TEST_SOURCE_TRACE", trace)
	// A failed optional read must leave the verified source and output usable.
	a.cfg.SourceTimeoutSeconds = 1
	sourceFault(t, "flac-info", "Album/01.flac", 10000, 0)
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal("optional specification failure stopped the scan", err)
	}
	if failed := read(); failed.Error != "" || failed.Hash != original.Hash || failed.BuiltHash != original.BuiltHash || failed.SourceAudio != nil {
		t.Fatal("optional specification failure invalidated verified audio", failed)
	}
	t.Setenv("MUSICFORGE_TEST_SOURCE_FAULT", "")
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	backfilled := read()
	if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind IN ('convert','move')").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after || backfilled.Hash != original.Hash || backfilled.BuiltHash != original.BuiltHash || backfilled.SourceBitrate != original.SourceBitrate || backfilled.SourceAudio == nil || *backfilled.SourceAudio != *original.SourceAudio || backfilled.OutputSize == nil || *backfilled.OutputSize != info.Size() {
		t.Fatal("backfill rebuilt audio or lost measured information", backfilled)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal("cached source specification was not reused", err)
	}
	operations, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(operations), "flac-info:Album/01.flac") != 2 || strings.Contains(string(operations), "hash:") || strings.Contains(string(operations), "stage:") {
		t.Fatal("backfill repeated full source reads or ignored the specification cache", string(operations), err)
	}
	// Older indexes with no measured bitrate still need only STREAMINFO.
	if _, err = a.db.Exec("DELETE FROM meta WHERE key GLOB 'source-media:*'"); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	if lightweight := read(); lightweight.SourceBitrate != 0 || lightweight.SourceAudio == nil || *lightweight.SourceAudio != *original.SourceAudio {
		t.Fatal("header-only refresh invented a compressed bitrate or lost the specification", lightweight)
	}
	// The next incremental scan must reuse even an unavailable-rate cache, and
	// ordinary library reads must work without opening either music directory.
	if err = a.rememberSourceMedia(backfilled, probeData{}); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal("cached source was probed again", err)
	}
	if unknown := read(); unknown.SourceBitrate != 0 || !unknown.mediaInfoKnown {
		t.Fatal("unknown bitrate was not cached", unknown)
	}
	for _, path := range []string{s.Source, s.Output} {
		if err = os.Rename(path, path+".offline"); err != nil {
			t.Fatal(err)
		}
	}
	if offline := read(); offline.SourceBitrate != 0 || offline.OutputSize == nil || *offline.OutputSize != info.Size() {
		t.Fatal("browsing depends on music mounts", offline)
	}
	if _, err = a.db.Exec("UPDATE sources SET hash=hash||'-changed'"); err != nil {
		t.Fatal(err)
	}
	if stale := read(); stale.mediaInfoKnown {
		t.Fatal("changed content used a previous bitrate", stale)
	}
	if _, err = a.db.Exec("UPDATE sources SET hash=?,mtime=mtime+1", original.Hash); err != nil {
		t.Fatal(err)
	}
	if stale := read(); stale.SourceBitrate != 0 || stale.mediaInfoKnown {
		t.Fatal("stale source measurement was shown", stale)
	}
	if _, err = a.db.Exec("UPDATE managed SET signature='replacement' WHERE kind='audio'"); err != nil {
		t.Fatal(err)
	}
	if stale := read(); stale.OutputSize != nil {
		t.Fatal("replacement used previous artifact size", stale)
	}
	if err = a.setMeta("audio-size:"+strconv.FormatInt(original.ID, 10), "invalid optional annotation"); err != nil {
		t.Fatal(err)
	}
	if malformed := read(); malformed.OutputSize != nil {
		t.Fatal("invalid annotation supplied an output size", malformed)
	}
}

func TestProbeAudioSpecificationUsesFirstAudioAndReportedBitDepth(t *testing.T) {
	for _, fixture := range []struct {
		raw  string
		want audioFormat
	}{
		{`{"streams":[{"codec_type":"video","codec_name":"mjpeg","bits_per_raw_sample":"8"},{"codec_type":"audio","codec_name":"flac","sample_rate":"96000","bits_per_raw_sample":"24","bits_per_sample":32,"channels":2}]}`, audioFormat{Codec: "flac", SampleRate: 96000, BitDepth: 24, Channels: 2}},
		{`{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"44100","bits_per_sample":16,"channels":2}]}`, audioFormat{Codec: "pcm_s16le", SampleRate: 44100, BitDepth: 16, Channels: 2}},
		{`{"streams":[{"codec_type":"audio","codec_name":"mp3","sample_rate":"48000","bits_per_raw_sample":"N/A","channels":2},{"codec_type":"audio","codec_name":"flac","bits_per_raw_sample":"24"}]}`, audioFormat{Codec: "mp3", SampleRate: 48000, Channels: 2}},
		{`{}`, audioFormat{}},
	} {
		var p probeData
		if err := json.Unmarshal([]byte(fixture.raw), &p); err != nil || *p.audioFormat() != fixture.want {
			t.Fatal("incorrect source specification", fixture, p.audioFormat(), err)
		}
	}
}

func TestProbeBitrateUsesAudioThenContainerAndKeepsUnknownUnknown(t *testing.T) {
	for _, fixture := range []struct {
		raw  string
		want int64
	}{
		{`{"streams":[{"codec_type":"video","bit_rate":"50000"},{"codec_type":"audio","bit_rate":"192000"}],"format":{"bit_rate":"200000"}}`, 192000},
		{`{"streams":[{"codec_type":"audio","bit_rate":"N/A"}],"format":{"bit_rate":"901234"}}`, 901234},
		{`{"streams":[{"codec_type":"audio","bit_rate":"-1"}],"format":{"bit_rate":"N/A"}}`, 0},
	} {
		var p probeData
		if err := json.Unmarshal([]byte(fixture.raw), &p); err != nil || p.bitrate() != fixture.want {
			t.Fatal("incorrect source bitrate", fixture, p.bitrate(), err)
		}
	}
}
