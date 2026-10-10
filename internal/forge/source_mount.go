package forge

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// source_root stays the schema-2 device:inode value. This optional witness is
// tied to that acknowledged value, so older Settings saves remain authoritative.
type sourceMountWitness struct {
	Identity  string `json:"identity"`
	Signature string `json:"signature"`
}

func sourceMountSignature(path string, info fs.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	// Linux dev_t encoding; match the actual device to ignore hidden overmounts.
	device := uint64(stat.Dev)
	major := (device >> 8 & 0xfff) | (device >> 32 & 0xfffff000)
	minor := (device & 0xff) | (device >> 12 & 0xffffff00)
	return rcloneMountSignature(string(mounts), path, fmt.Sprintf("%d:%d", major, minor))
}

func rcloneMountSignature(mounts, path, device string) string {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var point, root, filesystem, source string
	for _, line := range strings.Split(mounts, "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields, kind := strings.Fields(before), strings.Fields(after)
		if len(fields) < 6 || len(kind) < 3 || fields[2] != device {
			continue
		}
		mountpoint := unescape.Replace(fields[4])
		if !filepath.IsAbs(mountpoint) || !containsPath(mountpoint, path) || len(mountpoint) < len(point) {
			continue
		}
		point, root, filesystem, source = mountpoint, unescape.Replace(fields[3]), kind[0], unescape.Replace(kind[1])
	}
	// ponytail: only named rclone remotes have a reconnect identity we can trust.
	// Other filesystems and generic --devname labels retain strict device:inode checks.
	if filesystem != "fuse.rclone" || !strings.Contains(source, ":") || !filepath.IsAbs(root) {
		return ""
	}
	rel, err := filepath.Rel(point, path)
	if err != nil {
		return ""
	}
	value, _ := json.Marshal([]string{filesystem, source, filepath.Join(root, rel)})
	// Mount sources can contain inline credentials; persist only their digest.
	return digest(string(value))
}

func (a *App) verifySourceRoot(source sourceInfo) error {
	a.sourceRootMu.Lock()
	defer a.sourceRootMu.Unlock()
	expected, err := a.meta("source_root")
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	changed := later("Source mount changed; verify the mount and save Settings to acknowledge it", 30)
	if err == sql.ErrNoRows {
		return changed
	}
	var witness sourceMountWitness
	raw, err := a.meta("source_mount")
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if json.Unmarshal([]byte(raw), &witness) != nil {
		witness = sourceMountWitness{}
	}
	known := witness.Identity == expected && witness.Signature != ""
	if known && source.Mount != witness.Signature {
		return changed
	}
	if source.Identity == expected {
		if source.Mount != "" && !known {
			// Learn an optional witness only from a still-acknowledged root.
			return a.recordSourceRoot(source)
		}
		return nil
	}
	if !known || source.Mount == "" {
		return changed
	}
	if err = a.recordSourceRoot(source); err != nil {
		return err
	}
	a.logger.Info("source mount reconnected", "source", source.Path, "previous_identity", expected, "identity", source.Identity)
	return nil
}

// Caller holds sourceRootMu. Update both values together so old readers always
// see a valid source_root and a reconnect cannot trust a partly written witness.
func (a *App) recordSourceRoot(source sourceInfo) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('source_root',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", source.Identity); err != nil {
		return err
	}
	if source.Mount == "" {
		_, err = tx.Exec("DELETE FROM meta WHERE key='source_mount'")
	} else {
		raw, _ := json.Marshal(sourceMountWitness{Identity: source.Identity, Signature: source.Mount})
		_, err = tx.Exec("INSERT INTO meta(key,value) VALUES('source_mount',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(raw))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
