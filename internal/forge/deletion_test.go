package forge

import (
 "context"
 "database/sql"
 "encoding/json"
 "io"
 "log/slog"
 "os"
 "path/filepath"
 "strconv"
 "testing"
 "testing/fstest"
)

func TestDeletionJournalRecoversAfterUnlinkAndUnregister(t *testing.T) {
 for _, unregister := range []bool{false, true} {
  t.Run(strconv.FormatBool(unregister), func(t *testing.T) {
   a, s := testApp(t)
   path := makeFLAC(t, a, s, "Album/01.flac", "Track", false)
   if err := a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
   drain(t, a, true)
   source, err := a.sourceRel("Album/01.flac")
   if err != nil { t.Fatal(err) }
   if err = os.Remove(path); err != nil { t.Fatal(err) }
   if err = a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
   entry := deletion{source.ID, source.Output}
   raw, err := json.Marshal(entry)
   if err != nil { t.Fatal(err) }
   key := "deletion:"+strconv.FormatInt(source.ID, 10)+":"+digest(source.Output)
   if err = a.setMeta(key, string(raw)); err != nil { t.Fatal(err) }
   if err = os.Remove(filepath.Join(s.Output, source.Output)); err != nil { t.Fatal(err) }
   if unregister {
    if _, err = a.db.Exec("DELETE FROM managed WHERE path=?", source.Output); err != nil { t.Fatal(err) }
   }
   cfg := a.cfg
   a.Close()
   restored, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", fstest.MapFS{"index.html": {Data: []byte("test")}})
   if err != nil { t.Fatal(err) }
   defer restored.Close()
   if err = restored.ensureRecovery(context.Background(), s); err != nil { t.Fatal(err) }
   current, err := restored.source(source.ID)
   if err != nil || current.Output != "" || current.OutputPresent { t.Fatalf("deletion recovery left stale index: %+v / %v", current, err) }
   var pending, dirty int
   if err = restored.db.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'deletion:%'").Scan(&pending); err != nil { t.Fatal(err) }
   if err = restored.db.QueryRow("SELECT count(*) FROM dirty_dirs WHERE path='Album'").Scan(&dirty); err != nil { t.Fatal(err) }
   if pending != 0 || dirty != 1 { t.Fatalf("recovery lost cleanup/refresh: %d / %d", pending, dirty) }
   if err = restored.recoverDeletions(s); err != nil { t.Fatal("recovery replay must be idempotent:", err) }
  })
 }
}

func TestDeletionRecoveryPreservesUnregisteredReplacement(t *testing.T) {
 a, s := testApp(t)
 entry := deletion{123, "foreign.opus"}
 raw, err := json.Marshal(entry)
 if err != nil { t.Fatal(err) }
 if err = a.setMeta("deletion:123:"+digest(entry.Rel), string(raw)); err != nil { t.Fatal(err) }
 path := filepath.Join(s.Output, entry.Rel)
 if err = os.WriteFile(path, []byte("foreign replacement"), 0600); err != nil { t.Fatal(err) }
 if err = a.recoverDeletions(s); err == nil { t.Fatal("unregistered replacement was accepted for deletion") }
 actual, err := os.ReadFile(path)
 if err != nil || string(actual) != "foreign replacement" { t.Fatalf("foreign file was touched: %v", err) }
}

func TestDeletingRootTrackPreservesSharedArtwork(t *testing.T) {
 a, s := testApp(t)
 path := makeFLAC(t, a, s, "01.flac", "First", true)
 makeFLAC(t, a, s, "02.flac", "Second", true)
 if err := a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
 drain(t, a, true)
 source, err := a.sourceRel("01.flac")
 if err != nil { t.Fatal(err) }
 if err = os.Remove(path); err != nil { t.Fatal(err) }
 if err = a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
 if err = a.deleteExpired([]int64{source.ID}); err != nil { t.Fatal(err) }
 if _, err = os.Stat(filepath.Join(s.Output, "cover.jpg")); err != nil { t.Fatal("deleted shared root artwork:", err) }
 remaining, err := a.sourceRel("02.flac")
 if err != nil || !remaining.OutputPresent { t.Fatalf("other track was changed: %v", err) }
}

func TestClaimsSerializeSameSourceAcrossBuildTargets(t *testing.T) {
 a, s := testApp(t)
 makeFLAC(t, a, s, "Album/01.flac", "Track", false)
 if err := a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
 source, err := a.sourceRel("Album/01.flac")
 if err != nil { t.Fatal(err) }
 if _, err = a.queueBuild(source, Encoding{"mp3", "vbr", 192, 2}, false, true); err != nil { t.Fatal(err) }
 first, err := a.claim(true)
 if err != nil { t.Fatal(err) }
 if _, err = a.claim(true); err != sql.ErrNoRows { t.Fatalf("parallel profiles claimed the same source: %v", err) }
 if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", first.ID); err != nil { t.Fatal(err) }
 if _, err = a.claim(true); err != nil { t.Fatal("second profile did not become claimable:", err) }
}
