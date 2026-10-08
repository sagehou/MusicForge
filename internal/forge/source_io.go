package forge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Source mount calls can block inside FUSE. Keep them in a disposable process,
// without a database connection; this mode only reads the supplied source.
type sourceInfo struct {
	Path     string      `json:"path"`
	Rel      string      `json:"rel"`
	Length   int64       `json:"size"`
	Modified int64       `json:"mtime"`
	Bits     fs.FileMode `json:"mode"`
	Identity string      `json:"identity,omitempty"`
}

func (s sourceInfo) Name() string       { return filepath.Base(s.Path) }
func (s sourceInfo) Size() int64        { return s.Length }
func (s sourceInfo) ModTime() time.Time { return time.Unix(0, s.Modified) }
func (s sourceInfo) Mode() fs.FileMode  { return s.Bits }
func (s sourceInfo) IsDir() bool        { return s.Bits.IsDir() }
func (s sourceInfo) Sys() any           { return nil }

func sourceSnapshot(path, rel string, info fs.FileInfo) sourceInfo {
	s := sourceInfo{Path: path, Rel: rel, Length: info.Size(), Modified: info.ModTime().UnixNano(), Bits: info.Mode()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		s.Identity = fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	}
	return s
}

type sourceEvent struct {
	Info     *sourceInfo `json:"info,omitempty"`
	Path     string      `json:"path,omitempty"`
	Read     int64       `json:"read,omitempty"`
	Hash     string      `json:"hash,omitempty"`
	Error    string      `json:"error,omitempty"`
	Missing  bool        `json:"missing,omitempty"`
	Complete bool        `json:"complete,omitempty"`
}

// RunSourceIO handles only the internal child-process mode, before app startup.
func RunSourceIO(args []string) bool {
	if len(args) == 0 || args[0] != "-source-io" {
		return false
	}
	encoder := json.NewEncoder(os.Stdout)
	emit := func(e sourceEvent) error { return encoder.Encode(e) }
	var err error
	if len(args) != 4 {
		err = errors.New("invalid source I/O invocation")
	} else {
		err = readSourceIO(args[1], args[2], args[3], emit)
	}
	if err != nil {
		_ = emit(sourceEvent{Error: err.Error(), Missing: os.IsNotExist(err)})
	} else {
		_ = emit(sourceEvent{Complete: true})
	}
	return true
}

func readSourceIO(operation, root, rel string, emit func(sourceEvent) error) error {
	path, err := safePath(root, rel)
	if err != nil {
		return err
	}
	switch operation {
	case "stat":
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		snapshot := sourceSnapshot(path, rel, info)
		return emit(sourceEvent{Info: &snapshot})
	case "walk":
		base, err := safePath(root, ".")
		if err != nil {
			return err
		}
		if _, err = os.Stat(path); os.IsNotExist(err) && rel != "." {
			// A removed scoped album is valid; disappearance during traversal is not.
			return nil
		} else if err != nil {
			return err
		}
		return filepath.WalkDir(path, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("source symlink is unsupported: %s", path)
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			if d.IsDir() {
				return emit(sourceEvent{Path: rel})
			}
			if !strings.EqualFold(filepath.Ext(path), ".flac") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("not a regular file: %s", path)
			}
			snapshot := sourceSnapshot(path, rel, info)
			return emit(sourceEvent{Info: &snapshot, Path: rel})
		})
	case "hash":
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("source is not a regular file")
		}
		snapshot := sourceSnapshot(path, rel, info)
		if err = emit(sourceEvent{Info: &snapshot}); err != nil {
			return err
		}
		h := sha256.New()
		buf := make([]byte, 128*1024)
		var read int64
		last := time.Time{}
		for {
			n, readErr := f.Read(buf)
			if n > 0 {
				_, _ = h.Write(buf[:n])
				read += int64(n)
			}
			if time.Since(last) >= 250*time.Millisecond && n > 0 {
				if err = emit(sourceEvent{Read: read, Info: &snapshot}); err != nil {
					return err
				}
				last = time.Now()
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		return emit(sourceEvent{Hash: hex.EncodeToString(h.Sum(nil)), Read: read, Info: &snapshot})
	default:
		return errors.New("unknown source I/O operation")
	}
}

func (a *App) sourceTimeout() time.Duration {
	if a.cfg.SourceTimeoutSeconds > 0 {
		return time.Duration(a.cfg.SourceTimeoutSeconds) * time.Second
	}
	return 120 * time.Second
}

func (a *App) sourceIO(ctx context.Context, operation, root, rel string, receive func(sourceEvent) error) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.NewTimer(a.sourceTimeout())
	defer timer.Stop()
	// Bound unreaped helpers even if the kernel cannot release a hung mount call.
	select {
	case a.sourceSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("source I/O workers are still blocked: %w", context.DeadlineExceeded)
	}
	cmd := exec.CommandContext(child, executable, "-source-io", operation, root, rel)
	cmd.WaitDelay = 2 * time.Second
	var stderr boundedLog
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		<-a.sourceSlots
		return err
	}
	defer stdout.Close()
	events := make(chan sourceEvent)
	done := make(chan error, 1)
	go func() {
		defer func() { <-a.sourceSlots }()
		decoder := json.NewDecoder(stdout)
		var readErr error
		for {
			var event sourceEvent
			if readErr = decoder.Decode(&event); readErr != nil {
				break
			}
			select {
			case events <- event:
			case <-child.Done():
				readErr = child.Err()
			}
			if readErr != nil {
				break
			}
		}
		close(events)
		if readErr != io.EOF {
			cancel()
		}
		waitErr := cmd.Wait()
		if readErr == io.EOF {
			readErr = nil
		}
		done <- errors.Join(readErr, waitErr)
	}()
	complete := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("source %s stalled at %s (no progress for %s): %w", operation, rel, a.sourceTimeout(), context.DeadlineExceeded)
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			timer.Reset(a.sourceTimeout())
			if event.Error != "" {
				if event.Missing {
					return &os.PathError{Op: operation, Path: filepath.Join(root, rel), Err: os.ErrNotExist}
				}
				return errors.New(event.Error)
			}
			complete = complete || event.Complete
			if err = receive(event); err != nil {
				return err
			}
		case err = <-done:
			if err != nil {
				return fmt.Errorf("source %s failed at %s: %w", operation, rel, err)
			}
			if !complete {
				return errors.New("source I/O ended without a complete result")
			}
			return nil
		}
	}
}

func (a *App) sourceStat(ctx context.Context, root, rel string) (sourceInfo, error) {
	var info sourceInfo
	err := a.sourceIO(ctx, "stat", root, rel, func(e sourceEvent) error {
		if e.Info != nil {
			info = *e.Info
		}
		return nil
	})
	return info, err
}

func (a *App) sourceHash(ctx context.Context, root, rel string, progress func(int64, int64)) (string, error) {
	var hash string
	err := a.sourceIO(ctx, "hash", root, rel, func(e sourceEvent) error {
		if e.Info != nil && progress != nil {
			progress(e.Read, e.Info.Length)
		}
		if e.Hash != "" {
			hash = e.Hash
		}
		return nil
	})
	return hash, err
}
