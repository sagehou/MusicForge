package forge

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestUnchangedArtworkSkipsProbeAndExternalUpdatesDoNotEncode(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		t.Run(strconv.FormatBool(embedded), func(t *testing.T) {
			a, s := testApp(t)
			makeFLAC(t, a, s, "Album/01.flac", "Track", embedded)
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			source, _ := a.sourceRel("Album/01.flac")
			audioHash, err := fileHash(context.Background(), filepath.Join(s.Output, source.Output))
			if err != nil {
				t.Fatal(err)
			}
			a.cfg.FFprobe = "/bin/false"
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal("unchanged source or artwork was re-probed:", err)
			}
			external := filepath.Join(s.Source, "Album/cover.jpg")
			writeCover(t, external)
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(s.Output, "Album/cover.jpg")); err != nil {
				t.Fatal("external artwork did not update")
			}
			if err = os.Remove(external); err != nil {
				t.Fatal(err)
			}
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal("cached embedded fallback required another probe:", err)
			}
			if !embedded {
				if _, err = os.Stat(filepath.Join(s.Output, "Album/cover.jpg")); !os.IsNotExist(err) {
					t.Fatal("removed external cover was retained")
				}
			}
			afterHash, err := fileHash(context.Background(), filepath.Join(s.Output, source.Output))
			if err != nil || audioHash != afterHash {
				t.Fatal("artwork update re-encoded audio")
			}
			var jobs int
			if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind='convert'").Scan(&jobs); err != nil || jobs != 1 {
				t.Fatal("artwork update queued audio conversion")
			}
		})
	}
}
