package forge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	// An older schema-2 writer leaves the index intact but need not create caches.
	if _, err = a.db.Exec("DELETE FROM meta WHERE key LIKE 'source-media:%' OR key LIKE 'audio-size:%'"); err != nil {
		t.Fatal(err)
	}
	if old := read(); old.SourceBitrate != 0 || old.OutputSize != nil {
		t.Fatal("missing measurements were guessed", old)
	}
	var before, after int
	if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind IN ('convert','move')").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	backfilled := read()
	if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind IN ('convert','move')").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after || backfilled.Hash != original.Hash || backfilled.BuiltHash != original.BuiltHash || backfilled.SourceBitrate != original.SourceBitrate || backfilled.OutputSize == nil || *backfilled.OutputSize != info.Size() {
		t.Fatal("backfill rebuilt audio or lost measured information", backfilled)
	}
	// The next incremental scan must reuse even an unavailable-rate cache, and
	// ordinary library reads must work without opening either music directory.
	if err = a.rememberSourceMedia(backfilled, probeData{}); err != nil {
		t.Fatal(err)
	}
	a.cfg.FFprobe = "/nonexistent/ffprobe"
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
	if _, err = a.db.Exec("UPDATE sources SET mtime=mtime+1"); err != nil {
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
