package forge

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestStoppedBackupCheckAndRestore(t *testing.T) {
	a, settings := testApp(t)
	path := filepath.Join(t.TempDir(), "config.zip")
	if _, err := CreateBackup(a.cfg.ConfigDir, path, "test"); err == nil {
		t.Fatal("backed up a running application")
	}
	if err := os.MkdirAll(filepath.Join(a.cfg.ConfigDir, "edit-history"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.cfg.ConfigDir, "edit-history", "retained.lyrics"), []byte("[00:01]Saved"), 0600); err != nil {
		t.Fatal(err)
	}
	instance, _ := a.meta("instance")
	a.Close()
	info, err := CreateBackup(a.cfg.ConfigDir, path, "v0.3.0")
	if err != nil || info.Instance != instance || info.Schema != 2 || info.Output != settings.Output {
		t.Fatal(info, err)
	}
	if _, err = CreateBackup(a.cfg.ConfigDir, path, "test"); err == nil {
		t.Fatal("overwrote an existing backup")
	}
	if checked, err := CheckBackup(path); err != nil || checked != info {
		t.Fatal(checked, err)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	if restored, err := RestoreBackup(path, restoredDir); err != nil || restored != info {
		t.Fatal(restored, err)
	}
	if content, err := os.ReadFile(filepath.Join(restoredDir, "edit-history", "retained.lyrics")); err != nil || string(content) != "[00:01]Saved" {
		t.Fatal(string(content), err)
	}
	if _, err = RestoreBackup(path, restoredDir); err == nil {
		t.Fatal("restored over an existing configuration")
	}
	cfg := a.cfg
	cfg.ConfigDir = restoredDir
	reopened, err := New(cfg, a.logger, "test", a.assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if actual, err := reopened.settings(); err != nil || actual.Source != settings.Source || actual.Output != settings.Output {
		t.Fatal(actual, err)
	}
	// Invalid archives must fail before installing any configuration files.
	bad := filepath.Join(t.TempDir(), "invalid.zip")
	file, err := os.Create(bad)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, _ := archive.Create("config/../outside")
	_, _ = entry.Write([]byte("invalid"))
	_ = archive.Close()
	_ = file.Close()
	if _, err = RestoreBackup(bad, filepath.Join(t.TempDir(), "empty")); err == nil {
		t.Fatal("accepted an archive path outside config")
	}
}
