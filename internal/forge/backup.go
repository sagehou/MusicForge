package forge

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type BackupInfo struct {
	Format   int    `json:"format"`
	Version  string `json:"version"`
	Schema   int    `json:"schema"`
	Created  int64  `json:"created"`
	Instance string `json:"instance"`
	Source   string `json:"source"`
	Output   string `json:"output"`
}

func lockConfiguration(dir string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("another MusicForge process owns /config; stop it first")
	}
	return lock, nil
}

func inspectBackupConfig(dir string) (BackupInfo, error) {
	info := BackupInfo{Format: 1}
	db, err := openReaders(filepath.Join(dir, "musicforge.db"))
	if err != nil {
		return info, err
	}
	defer db.Close()
	if err = db.QueryRow("PRAGMA user_version").Scan(&info.Schema); err != nil {
		return info, err
	}
	if info.Schema != 2 {
		return info, fmt.Errorf("unsupported backup schema %d; expected 2", info.Schema)
	}
	var check string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		return info, err
	}
	if check != "ok" {
		return info, fmt.Errorf("backup database integrity check: %s", check)
	}
	if err = db.QueryRow("SELECT value FROM meta WHERE key='instance'").Scan(&info.Instance); err != nil {
		return info, err
	}
	var raw string
	if err = db.QueryRow("SELECT data FROM settings WHERE id=1").Scan(&raw); err != nil {
		return info, err
	}
	var settings Settings
	if err = json.Unmarshal([]byte(raw), &settings); err != nil {
		return info, err
	}
	info.Source, info.Output = settings.Source, settings.Output
	return info, nil
}

// Back up a stopped config, including any WAL and retained edit snapshots.
// Output/source files and externally mounted credentials are separate backups.
func CreateBackup(dir, destination, version string) (BackupInfo, error) {
	lock, err := lockConfiguration(dir)
	if err != nil {
		return BackupInfo{}, err
	}
	defer lock.Close()
	info, err := inspectBackupConfig(dir)
	if err != nil {
		return info, err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return info, err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return info, err
	}
	absolute, err = filepath.Abs(filepath.Dir(destination))
	if err != nil {
		return info, err
	}
	parent, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return info, err
	}
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return info, errors.New("store the backup outside the configuration directory")
	}
	info.Version, info.Created = version, time.Now().Unix()
	file, err := os.CreateTemp(parent, ".musicforge-backup-*")
	if err != nil {
		return info, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	archive := zip.NewWriter(file)
	manifest, _ := json.Marshal(info)
	entry, err := archive.Create("manifest.json")
	if err == nil {
		_, err = entry.Write(manifest)
	}
	if err == nil {
		err = filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == "." {
				return err
			}
			if rel == "source-staging" && item.IsDir() {
				return filepath.SkipDir
			}
			if rel == "instance.lock" || rel == "musicforge.db-shm" || item.IsDir() {
				return nil
			}
			stat, err := item.Info()
			if err != nil {
				return err
			}
			if !stat.Mode().IsRegular() {
				return fmt.Errorf("backup requires regular files: %s", rel)
			}
			header, err := zip.FileInfoHeader(stat)
			if err != nil {
				return err
			}
			header.Name, header.Method = "config/"+filepath.ToSlash(rel), zip.Deflate
			header.SetMode(0600)
			entry, err := archive.CreateHeader(header)
			if err != nil {
				return err
			}
			input, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(entry, input)
			return errors.Join(copyErr, input.Close())
		})
	}
	err = errors.Join(err, archive.Close())
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = os.Link(file.Name(), destination) // Never replace an existing backup.
	}
	if err == nil {
		err = syncDirectory(parent)
	}
	return info, err
}

func unpackBackup(path, destination string) (BackupInfo, error) {
	info := BackupInfo{}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return info, err
	}
	defer archive.Close()
	manifestFound := false
	for _, entry := range archive.File {
		if entry.Name == "manifest.json" {
			if manifestFound || entry.UncompressedSize64 > 65536 {
				return info, errors.New("invalid backup manifest")
			}
			input, err := entry.Open()
			if err != nil {
				return info, err
			}
			raw, readErr := io.ReadAll(input)
			err = errors.Join(readErr, input.Close())
			if err == nil {
				err = json.Unmarshal(raw, &info)
			}
			if err != nil {
				return info, err
			}
			manifestFound = true
			continue
		}
		rel := strings.TrimPrefix(entry.Name, "config/")
		if rel == entry.Name || !fs.ValidPath(rel) || strings.Contains(rel, "\\") || !entry.Mode().IsRegular() ||
			rel == "instance.lock" || rel == "source-staging" || strings.HasPrefix(rel, "source-staging/") {
			return info, fmt.Errorf("invalid backup entry: %s", entry.Name)
		}
		path := filepath.Join(destination, filepath.FromSlash(rel))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return info, err
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return info, err
		}
		input, err := entry.Open()
		if err != nil {
			output.Close()
			return info, err
		}
		_, copyErr := io.Copy(output, input) // Reading to EOF verifies the ZIP CRC.
		if err = errors.Join(copyErr, input.Close(), output.Sync(), output.Close()); err != nil {
			return info, err
		}
	}
	if !manifestFound || info.Format != 1 {
		return info, errors.New("unsupported or missing backup manifest")
	}
	actual, err := inspectBackupConfig(destination)
	if err == nil && (actual.Schema != info.Schema || actual.Instance != info.Instance || actual.Source != info.Source || actual.Output != info.Output) {
		err = errors.New("backup manifest does not match the database")
	}
	return info, err
}

func CheckBackup(path string) (BackupInfo, error) {
	dir, err := os.MkdirTemp("", "musicforge-backup-check-*")
	if err != nil {
		return BackupInfo{}, err
	}
	defer os.RemoveAll(dir)
	return unpackBackup(path, dir)
}

// Restore into an empty configuration directory; existing data is never replaced.
func RestoreBackup(path, dir string) (info BackupInfo, err error) {
	if err = os.MkdirAll(dir, 0700); err != nil {
		return info, err
	}
	lock, err := lockConfiguration(dir)
	if err != nil {
		return info, err
	}
	defer lock.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return info, err
	}
	for _, entry := range entries {
		if entry.Name() != "instance.lock" {
			return info, errors.New("restore requires an empty configuration directory; keep the current config as a backup")
		}
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return info, err
	}
	stage, err := os.MkdirTemp(dir, ".restore-*")
	if err != nil {
		return info, err
	}
	defer os.RemoveAll(stage)
	if info, err = unpackBackup(path, stage); err != nil {
		return info, err
	}
	entries, err = os.ReadDir(stage)
	if err != nil {
		return info, err
	}
	installed := []string{}
	defer func() {
		if err != nil {
			for _, path := range installed {
				_ = os.RemoveAll(path)
			}
		}
	}()
	for _, entry := range entries {
		destination := filepath.Join(dir, entry.Name())
		if err = os.Rename(filepath.Join(stage, entry.Name()), destination); err != nil {
			return info, err
		}
		installed = append(installed, destination)
	}
	err = syncDirectory(dir)
	return info, err
}
