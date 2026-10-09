package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type stagedSourceKey struct{}

// Scratch files are disposable and never part of artifact recovery or schema 2.
type stagedSource struct {
	Path string
	Rel  string
	Hash string
	size int64
	app  *App
}

func (s *stagedSource) Close() {
	_ = os.Remove(s.Path)
	s.app.stagingMu.Lock()
	s.app.stagingBytes -= s.size
	s.app.stagingMu.Unlock()
}

func (a *App) initializeStaging() error {
	a.stagingDir = filepath.Join(a.cfg.ConfigDir, "source-staging")
	if err := os.Mkdir(a.stagingDir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(a.stagingDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("source-staging must be a local directory, without a symlink")
	}
	if err = os.Chmod(a.stagingDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(a.stagingDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".musicforge-") && strings.HasSuffix(entry.Name(), ".source") && !entry.IsDir() {
			if err = os.Remove(filepath.Join(a.stagingDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) stageSource(ctx context.Context, settings Settings, source Source, job int64, activity Activity) (*stagedSource, error) {
	limit := a.cfg.StagingMaxBytes
	if limit == 0 {
		limit = 4 * 1024 * 1024 * 1024
	}
	if source.Size < 1 || source.Size > limit {
		return nil, fmt.Errorf("source requires %d staging bytes; MUSICFORGE_STAGING_MAX_BYTES is %d", source.Size, limit)
	}
	for {
		a.stagingMu.Lock()
		if source.Size <= limit-a.stagingBytes {
			var disk syscall.Statfs_t
			err := syscall.Statfs(a.stagingDir, &disk)
			if err == nil && uint64(source.Size)+64*1024*1024 > disk.Bavail*uint64(disk.Bsize) {
				err = errors.New("insufficient local disk space for source staging; keep at least 64 MiB free for /config")
			}
			if err != nil {
				a.stagingMu.Unlock()
				return nil, err
			}
			a.stagingBytes += source.Size
			a.stagingMu.Unlock()
			break
		}
		a.stagingMu.Unlock()
		activity.Phase = "staging_wait"
		a.reportProgress(job, activity, 0)
		if !pause(ctx, time.Second) {
			return nil, ctx.Err()
		}
	}
	staged := &stagedSource{Path: filepath.Join(a.stagingDir, ".musicforge-"+randomToken()+".source"), Rel: source.Rel, size: source.Size, app: a}
	f, err := os.OpenFile(staged.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		activity.Phase, activity.ReadTotalBytes = "read", source.Size
		a.reportProgress(job, activity, 0)
		last := time.Time{}
		err = a.sourceIO(ctx, "stage", settings.Source, source.Rel, func(event sourceEvent) error {
			if event.Hash != "" {
				staged.Hash = event.Hash
			}
			if time.Since(last) >= time.Second || event.Hash != "" {
				activity.ReadBytes = event.Read
				a.reportProgress(job, activity, float64(event.Read)/float64(source.Size)*.1)
				last = time.Now()
			}
			return nil
		}, staged.Path, strconv.FormatInt(source.Size, 10))
	}
	if err != nil {
		staged.Close()
		return nil, err
	}
	if staged.Hash == "" {
		staged.Close()
		return nil, errors.New("source staging completed without a full content hash")
	}
	return staged, nil
}
