package forge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRcloneMountSignatureIgnoresTransientIDsAndResolvesBindRoots(t *testing.T) {
	const initial = `41 1 0:54 / /music/source ro,nosuid shared:7 - fuse.rclone remote:Library ro,user_id=1000`
	expected := rcloneMountSignature(initial, "/music/source", "0:54")
	if len(expected) != 64 {
		t.Fatal("named rclone mount lacks a stable digest", expected)
	}
	for _, test := range []struct {
		name, mount, path, device string
		same                      bool
	}{
		{"reconnect", `93 8 0:87 / /music/source rw - fuse.rclone remote:Library rw,user_id=12001`, "/music/source", "0:87", true},
		{"other remote", `93 8 0:87 / /music/source ro - fuse.rclone other:Library ro`, "/music/source", "0:87", false},
		{"other directory", `93 8 0:87 /Other /music/source ro - fuse.rclone remote:Library ro`, "/music/source", "0:87", false},
		{"missing mount", `1 0 8:1 / / rw - ext4 /dev/sda1 rw`, "/music/source", "8:1", false},
		{"generic label", `93 8 0:87 / /music/source ro - fuse.rclone rclone ro`, "/music/source", "0:87", false},
		{"prefix sibling", initial, "/music/source-other", "0:54", false},
		{"hidden mount", initial + "\n" + `92 8 0:80 / /music/source ro - fuse.rclone wrong:Library ro`, "/music/source", "0:54", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := rcloneMountSignature(test.mount, test.path, test.device)
			if (actual == expected) != test.same {
				t.Fatal("incorrect mount equivalence", actual)
			}
		})
	}
	parent := `41 1 0:54 / /mnt/rclone ro - fuse.rclone remote:Library ro`
	bind := `93 8 0:87 /Artist\040One /music/source ro - fuse.rclone remote:Library ro`
	if rcloneMountSignature(parent, "/mnt/rclone/Artist One", "0:54") != rcloneMountSignature(bind, "/music/source", "0:87") {
		t.Fatal("bind root and source subdirectory identify different libraries")
	}
	escaped := `93 8 0:87 / /music/source\040dir ro - fuse.rclone remote:Library\134Name ro`
	literal := `93 8 0:87 / /elsewhere ro - fuse.rclone remote:Library\Name ro`
	if rcloneMountSignature(escaped, "/music/source dir", "0:87") != rcloneMountSignature(literal, "/elsewhere", "0:87") {
		t.Fatal("mountinfo escapes changed identity")
	}
}

func TestSourceMountReconnectPreservesSchemaTwoAcknowledgmentAndRestart(t *testing.T) {
	a, s := testApp(t)
	root, err := a.sourceStat(context.Background(), s.Source, ".")
	if err != nil {
		t.Fatal(err)
	}
	// An upgrade may learn the new optional witness from the old acknowledged ID.
	root.Mount = digest("remote:Library")
	if err = a.verifySourceRoot(root); err != nil {
		t.Fatal("legacy acknowledged root could not establish witness", err)
	}
	root.Identity = "12345:1"
	if err = a.verifySourceRoot(root); err != nil {
		t.Fatal("same rclone library did not reconnect", err)
	}
	assertRoot := func(want string) {
		t.Helper()
		actual, err := a.meta("source_root")
		if err != nil || actual != want {
			t.Fatal("schema-2 device:inode acknowledgment changed", actual, err)
		}
	}
	assertRoot(root.Identity)
	for _, mount := range []string{"", digest("other:Library")} {
		changed := root
		changed.Mount = mount
		// Even a reused device:inode must not conceal a disappeared/wrong mount.
		var deferredErr *deferred
		if err = a.verifySourceRoot(changed); !errors.As(err, &deferredErr) || !strings.HasPrefix(err.Error(), "Source mount changed;") {
			t.Fatal("unconfirmed mount was accepted", err)
		}
		assertRoot(root.Identity)
	}
	a.Close()
	a, err = New(a.cfg, a.logger, "test-restart", a.assets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if online, _, at := a.storageStatus(s); online || at != 0 {
		t.Fatal("restart claimed storage was checked", online, at)
	}
	root.Identity = "67890:1"
	if err = a.verifySourceRoot(root); err != nil {
		t.Fatal("reconnect witness lost across restart", err)
	}
	assertRoot(root.Identity)
	// An older compatible Settings save changes source_root only. Its explicitly
	// acknowledged root overrides the stale witness, which cannot approve a remount.
	if err = a.setMeta("source_root", "24680:1"); err != nil {
		t.Fatal(err)
	}
	root.Mount = digest("newly-acknowledged:Library")
	if err = a.verifySourceRoot(root); err == nil {
		t.Fatal("stale witness approved an unacknowledged root")
	}
	root.Identity = "24680:1"
	if err = a.verifySourceRoot(root); err != nil {
		t.Fatal("older Settings acknowledgment was ignored", err)
	}
	root.Identity = "13579:1"
	if err = a.verifySourceRoot(root); err != nil {
		t.Fatal("new acknowledged remote cannot reconnect", err)
	}
	assertRoot(root.Identity)
	if err = a.initializeStorageContext(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err = a.meta("source_mount"); err != sql.ErrNoRows {
		t.Fatal("local source retained a stale rclone witness", err)
	}
}

func TestSourceMountUnknownAndMalformedWitnessRequireAcknowledgment(t *testing.T) {
	for _, raw := range []string{"", "invalid", `{"identity":"1:1","signature":"different"}`, `{"identity":"1:1","signature":"same","bad":}`} {
		t.Run(fmt.Sprintf("witness:%s", raw), func(t *testing.T) {
			a, _ := testApp(t)
			if err := a.setMeta("source_root", "1:1"); err != nil {
				t.Fatal(err)
			}
			if err := a.setMeta("source_mount", raw); err != nil {
				t.Fatal(err)
			}
			if err := a.verifySourceRoot(sourceInfo{Identity: "2:1", Mount: "same"}); err == nil {
				t.Fatal("unconfirmed root accepted an absent/malformed/mismatched witness")
			}
			value, _ := a.meta("source_root")
			if value != "1:1" {
				t.Fatal("failed verification overwrote the last acknowledgment")
			}
		})
	}
}

func TestApproveCurrentMountPreservesSettingsAndOutputOwnership(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled:%t", enabled), func(t *testing.T) {
			a, s := testApp(t)
			s.Enabled = enabled
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			request := lyricsSession(t, a)
			before, _ := a.meta("source_root")
			markerPath := filepath.Join(s.Output, ".musicforge")
			marker, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(s.Source, s.Source+".previous"); err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(s.Source, 0755); err != nil {
				t.Fatal(err)
			}
			if err = a.storageContext(context.Background(), s); err == nil {
				t.Fatal("operation accepted an unapproved replacement mount")
			}
			body := map[string]string{"source": s.Source, "output": s.Output}
			stale := map[string]string{"source": s.Source + ".previous", "output": s.Output}
			if w := request("POST", "/api/maintenance/storage/acknowledge", stale); w.Code != 409 {
				t.Fatal("approved different paths than the user reviewed", w.Code, w.Body.String())
			}
			// An empty output that lost its marker must not be silently claimed.
			if err = os.Remove(markerPath); err != nil {
				t.Fatal(err)
			}
			if w := request("POST", "/api/maintenance/storage/acknowledge", body); w.Code != 409 {
				t.Fatal("approved an output without ownership", w.Code, w.Body.String())
			}
			if _, err = os.Stat(markerPath); !os.IsNotExist(err) {
				t.Fatal("approval recreated a missing ownership marker", err)
			}
			if root, _ := a.meta("source_root"); root != before {
				t.Fatal("failed approval changed the acknowledged source", root)
			}
			if err = os.WriteFile(markerPath, marker, 0600); err != nil {
				t.Fatal(err)
			}
			a.files.Lock()
			w := request("POST", "/api/maintenance/storage/acknowledge", body)
			a.files.Unlock()
			if w.Code != 409 {
				t.Fatal("approval raced with a library file operation", w.Code, w.Body.String())
			}
			if w = request("POST", "/api/maintenance/storage/acknowledge", body); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if root, _ := a.meta("source_root"); root == before {
				t.Fatal("explicit approval did not acknowledge the new source")
			}
			if got, err := a.settings(); err != nil || got != s {
				t.Fatal("approval changed settings or the background-jobs switch", got, err)
			}
			var jobs int
			if err = a.reads.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("approval queued an unexpected scan", jobs, err)
			}
			if online, message, at := a.storageStatus(s); !online || message != "" || at == 0 {
				t.Fatal("approval did not refresh observed availability", online, message, at)
			}
			if err = a.storageContext(context.Background(), s); err != nil {
				t.Fatal("operation did not accept the approved mount", err)
			}
		})
	}
}
