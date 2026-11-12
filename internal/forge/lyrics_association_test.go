package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLyricsAssociationRequiresReviewAndEnablesOutputOnlyConversion(t *testing.T) {
	for _, kind := range []string{"unregistered", "stale source", "retired owner", "modified", "invalid annotation"} {
		t.Run(kind, func(t *testing.T) {
			a, settings := testApp(t)
			source := seedLyricsArtifact(t, a, settings)
			ctx := context.Background()
			original := "[00:01]愛這風雲\n"
			if err := a.writeLyricsFile(ctx, settings, source, original, "unison"); err != nil {
				t.Fatal(err)
			}
			prior := LyricsStatus{State: "ready", Output: source.Output, Signature: digest("registered audio fixture"),
				Path: lyricsRel(source.Output), Provider: "unison", Checked: 123}
			raw, _ := json.Marshal(prior)
			if err := a.setMeta("lyrics-status:1", string(raw)); err != nil {
				t.Fatal(err)
			}
			key := lyricsFileKey(lyricsRel(source.Output))
			path := filepath.Join(settings.Output, lyricsRel(source.Output))
			var err error
			switch kind {
			case "unregistered":
				_, err = a.db.Exec("DELETE FROM meta WHERE key=?", key)
			case "stale source":
				_, err = a.db.Exec("UPDATE sources SET hash='rebuilt',built_hash='rebuilt' WHERE id=1")
			case "retired owner":
				_, err = a.db.Exec("UPDATE meta SET value=json_set(value,'$.source_id',999) WHERE key=?", key)
			case "modified":
				original = "\ufeff[ti:原有標題]\r\n[00:01]愛這風雲\r\n"
				err = os.WriteFile(path, []byte(original), 0644)
			case "invalid annotation":
				err = a.setMeta(key, "{")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			ownership, err := a.optionalMeta(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(settings.Source, settings.Source+"-offline"); err != nil {
				t.Fatal(err)
			}
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				t.Error("association contacted a lyrics provider", r.URL)
				w.WriteHeader(500)
			})
			if status := a.simplifyLyrics(ctx, settings, source.ID, ""); status.State != "failed" {
				t.Fatal("ordinary conversion adopted lyrics without confirmation", status)
			}
			request := lyricsSession(t, a)
			w := request("GET", "/api/library/1/lyrics/association", nil)
			var review lyricsAssociation
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &review) != nil || !review.Required || review.Text != original {
				t.Fatal(w.Code, w.Body.String())
			}
			if after, err := a.optionalMeta(ctx, key); err != nil || after != ownership {
				t.Fatal("review changed ownership", after, err)
			}
			if after, err := a.meta("lyrics-status:1"); err != nil || after != string(raw) {
				t.Fatal("review changed matching status", after, err)
			}
			if w = request("PUT", "/api/library/1/lyrics/association", map[string]string{"fingerprint": "unreviewed"}); w.Code != 409 {
				t.Fatal("association accepted an unreviewed file", w.Code, w.Body.String())
			}
			if w = request("PUT", "/api/library/1/lyrics/association", map[string]string{"fingerprint": review.Fingerprint}); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			entry, text, present, err := a.readLyricsFile(ctx, settings, lyricsRel(source.Output))
			current, sourceErr := a.source(source.ID)
			if err != nil || sourceErr != nil || !present || text != original || entry.ID != source.ID || entry.SourceHash != current.BuiltHash {
				t.Fatal("association did not bind the reviewed text to the current song", entry, text, err, sourceErr)
			}
			if after, err := os.Stat(path); err != nil || !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("association rewrote the LRC", err)
			}
			if kind == "stale source" {
				if entry.Provider != "unison" {
					t.Fatal("association lost the original provider", entry)
				}
				if after, err := a.meta("lyrics-status:1"); err != nil || after != string(raw) {
					t.Fatal("association rewrote a still-current matching result", after, err)
				}
			} else if entry.Provider != "source" {
				t.Fatal("manually associated text kept unverified provider provenance", entry)
			}
			if status := a.simplifyLyrics(ctx, settings, source.ID, ""); status.State != "ready" || !status.replaced {
				t.Fatal("associated lyrics could not be converted", status)
			}
			_, text, _, err = a.readLyricsFile(ctx, settings, lyricsRel(source.Output))
			want := "[00:01]爱这风云\n"
			if kind == "modified" {
				want = "[ti:原有標題]\n" + want
			}
			if err != nil || text != want {
				t.Fatal(text, err)
			}
		})
	}
}

func TestLyricsAssociationRejectsChangesAfterReview(t *testing.T) {
	for _, kind := range []string{"lyrics", "audio", "ownership", "status"} {
		t.Run(kind, func(t *testing.T) {
			a, settings := testApp(t)
			source := seedLyricsArtifact(t, a, settings)
			path := filepath.Join(settings.Output, lyricsRel(source.Output))
			if err := os.WriteFile(path, []byte("[00:01]愛這風雲\n"), 0644); err != nil {
				t.Fatal(err)
			}
			request := lyricsSession(t, a)
			w := request("GET", "/api/library/1/lyrics/association", nil)
			var review lyricsAssociation
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &review) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			var err error
			switch kind {
			case "lyrics":
				err = os.WriteFile(path, []byte("[00:01]更新後的愛\n"), 0644)
			case "audio":
				_, err = a.db.Exec("UPDATE managed SET signature='new-audio' WHERE kind='audio'")
			case "ownership":
				err = a.setMeta(lyricsFileKey(lyricsRel(source.Output)), "{}")
			case "status":
				err = a.setMeta("lyrics-status:1", "{}")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := a.optionalMeta(context.Background(), lyricsFileKey(lyricsRel(source.Output)))
			if err != nil {
				t.Fatal(err)
			}
			w = request("PUT", "/api/library/1/lyrics/association", map[string]string{"fingerprint": review.Fingerprint})
			if w.Code != 409 {
				t.Fatal("association accepted a stale review", w.Code, w.Body.String())
			}
			if after, err := a.optionalMeta(context.Background(), lyricsFileKey(lyricsRel(source.Output))); err != nil || after != before {
				t.Fatal("rejected association changed ownership", after, err)
			}
		})
	}
}

func TestLyricsAssociationPreservesSymlinksAndAmbiguousPaths(t *testing.T) {
	for _, kind := range []string{"symlink", "shared audio path"} {
		t.Run(kind, func(t *testing.T) {
			a, settings := testApp(t)
			source := seedLyricsArtifact(t, a, settings)
			path := filepath.Join(settings.Output, lyricsRel(source.Output))
			original := "[00:01]愛這風雲\n"
			if err := os.WriteFile(path, []byte(original), 0644); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Rename(path, path+".user"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".user", path); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.db.Exec(`INSERT INTO sources(id,rel,hash,built_hash,output,output_present) VALUES(2,'Album/01.mp3','other','other','Album/01.mp3',1);
				 INSERT INTO managed(path,source_id,kind,signature) VALUES('Album/01.mp3',2,'audio','other')`); err != nil {
					t.Fatal(err)
				}
			}
			request := lyricsSession(t, a)
			w := request("GET", "/api/library/1/lyrics/association", nil)
			if w.Code != 409 {
				t.Fatal("review accepted an unsafe association", w.Code, w.Body.String())
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != original {
				t.Fatal("association changed a conflicting file", err)
			}
		})
	}
}
