package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpgradeRejectsStaleIndexDuringQuietPeriod(t *testing.T) {
	a, s := testApp(t)
	oldPath := makeFLAC(t, a, s, "Album/replaced.flac", "Old", false)
	replacement := makeFLAC(t, a, s, "Album/reused.flac", "Previous version", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	old, err := a.sourceRel("Album/replaced.flac")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	makeFLAC(t, a, s, "Album/reused.flac", "New version", false)
	now := time.Now()
	if err = os.Chtimes(replacement, now, now); err != nil {
		t.Fatal(err)
	}
	var waiting *deferred
	if err = a.scan(context.Background(), ScanRequest{}); !errors.As(err, &waiting) {
		t.Fatalf("scan must wait for stable input: %v", err)
	}
	event := UpgradeRequest{New: []string{"Album/reused.flac"}, Old: []string{"Album/replaced.flac"}}
	if err = a.finishUpgrade(context.Background(), event); !errors.As(err, &waiting) {
		t.Fatalf("stale ready index permitted upgrade cleanup: %v", err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, old.Output)); err != nil {
		t.Fatalf("old playable artifact was deleted before the new source was indexed: %v", err)
	}
	quiet := time.Now().Add(-time.Minute)
	if err = os.Chtimes(replacement, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	if err = a.finishUpgrade(context.Background(), event); !errors.As(err, &waiting) {
		t.Fatalf("cleanup must still wait for conversion: %v", err)
	}
	drain(t, a, true)
	if err = a.finishUpgrade(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, old.Output)); !os.IsNotExist(err) {
		t.Fatalf("validated upgrade did not clean replaced artifact: %v", err)
	}
}

func TestScopedScanDistinguishesCrossDirectoryMoveFromCopy(t *testing.T) {
	for _, move := range []bool{true, false} {
		name := "copy"
		if move {
			name = "move"
		}
		t.Run(name, func(t *testing.T) {
			a, s := testApp(t)
			path := makeFLAC(t, a, s, "OldAlbum/01.flac", "Track", false)
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			old, err := a.sourceRel("OldAlbum/01.flac")
			if err != nil {
				t.Fatal(err)
			}
			artifactHash, err := fileHash(context.Background(), filepath.Join(s.Output, old.Output))
			if err != nil {
				t.Fatal(err)
			}
			newPath := filepath.Join(s.Source, "NewAlbum/01.flac")
			if err = os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
				t.Fatal(err)
			}
			if move {
				if err = os.Rename(path, newPath); err != nil {
					t.Fatal(err)
				}
			} else {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(newPath, content, 0600); err != nil {
					t.Fatal(err)
				}
				quiet := time.Now().Add(-time.Minute)
				if err = os.Chtimes(newPath, quiet, quiet); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.scan(context.Background(), ScanRequest{Dirs: []string{"NewAlbum"}}); err != nil {
				t.Fatal(err)
			}
			current, err := a.sourceRel("NewAlbum/01.flac")
			if err != nil {
				t.Fatal(err)
			}
			if move {
				if current.ID != old.ID {
					t.Fatal("scoped scan re-indexed the move as a duplicate")
				}
				drain(t, a, false)
				current, err = a.source(current.ID)
				if err != nil {
					t.Fatal(err)
				}
				hash, err := fileHash(context.Background(), filepath.Join(s.Output, current.Output))
				if err != nil || hash != artifactHash {
					t.Fatalf("move did not preserve artifact: %v", err)
				}
				var conversions int
				if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind='convert'").Scan(&conversions); err != nil {
					t.Fatal(err)
				}
				if conversions != 1 {
					t.Fatal("move queued redundant encoding")
				}
			} else {
				if current.ID == old.ID {
					t.Fatal("copy stole original source identity")
				}
				drain(t, a, true)
				original, err := a.source(old.ID)
				if err != nil || !original.Present || original.Output != old.Output {
					t.Fatalf("copy damaged original: %v", err)
				}
				if _, err = os.Stat(filepath.Join(s.Output, old.Output)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCorruptSourceDoesNotBlockHealthyTracks(t *testing.T) {
	for _, content := range []string{"", "not FLAC"} {
		t.Run("content="+content, func(t *testing.T) {
			a, s := testApp(t)
			expiredPath := makeFLAC(t, a, s, "Removed/01.flac", "Retained", false)
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			expired, err := a.sourceRel("Removed/01.flac")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(expiredPath); err != nil {
				t.Fatal(err)
			}
			corrupt := filepath.Join(s.Source, "00-corrupt.flac")
			if err = os.WriteFile(corrupt, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			quiet := time.Now().Add(-time.Minute)
			if err = os.Chtimes(corrupt, quiet, quiet); err != nil {
				t.Fatal(err)
			}
			makeFLAC(t, a, s, "Healthy/01.flac", "Healthy", false)
			if err = a.scan(context.Background(), ScanRequest{}); err == nil {
				t.Fatal("scan must report corrupt source")
			}
			damaged, err := a.sourceRel("00-corrupt.flac")
			if err != nil || damaged.Error == "" || damaged.Hash != "" {
				t.Fatalf("corrupt source lacks actionable indexed error: %+v / %v", damaged, err)
			}
			healthy, err := a.sourceRel("Healthy/01.flac")
			if err != nil {
				t.Fatalf("corrupt source prevented healthy indexing: %v", err)
			}
			drain(t, a, true)
			healthy, err = a.source(healthy.ID)
			if err != nil || !healthy.OutputPresent {
				t.Fatalf("healthy track did not build: %v", err)
			}
			retained, err := a.source(expired.ID)
			if err != nil || !retained.Present {
				t.Fatalf("incomplete scan incorrectly expired unseen source: %v", err)
			}
			makeFLAC(t, a, s, "00-corrupt.flac", "Repaired", false)
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			repaired, err := a.source(damaged.ID)
			if err != nil || repaired.Error != "" || !repaired.OutputPresent {
				t.Fatalf("repair failed to recover: %+v / %v", repaired, err)
			}
		})
	}
}
