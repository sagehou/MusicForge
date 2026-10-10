package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

func decodingFailure(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range []string{"decoding error:", "decode_frame() failed", "error while decoding", "error processing packet in decoder:", "reference flac decoder failed:"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// Classify existing text too, so older failed tasks gain readable summaries.
func failureCategory(message string) string {
	switch {
	case message == "", message == "Paused by administrator", message == "Stopped by administrator":
		return ""
	case strings.HasPrefix(message, "Output validation failed"):
		return "validate"
	case strings.HasPrefix(message, "Audio decoding failed"), decodingFailure(message):
		return "decode"
	case strings.Contains(message, "Source read unavailable:"), strings.Contains(message, "source preparation failed"), strings.Contains(message, "source stage failed"), strings.Contains(message, "source stage stalled"):
		return "read"
	case strings.HasPrefix(message, "Audio conversion failed"), strings.Contains(message, "ffmpeg failed:"):
		return "encode"
	default:
		return ""
	}
}

type conversionFailure struct {
	source Source
	stage  string
	cause  error
}

func (e *conversionFailure) Error() string {
	label := "Audio conversion failed"
	if e.stage == "decode" {
		label = "Audio decoding failed"
	} else if e.stage == "validate" {
		label = "Output validation failed"
	}
	return fmt.Sprintf("%s\nSource: %q\nSource SHA-256: %s\nSource bytes: %d\n%s", label, e.source.Rel, e.source.Hash, e.source.Size, e.cause)
}

func (e *conversionFailure) Unwrap() error { return e.cause }

func nativeFLAC(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	_, err = io.ReadFull(f, magic[:])
	return err == nil && string(magic[:]) == "fLaC"
}

func (a *App) logMediaVersions(ctx context.Context) {
	a.mediaOnce.Do(func() {
		versions := make([]any, 0, 4)
		for _, tool := range []struct{ name, path, flag string }{{"ffmpeg", a.cfg.FFmpeg, "-version"}, {"flac", "flac", "--version"}} {
			versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			out, err := a.mediaTool(versionCtx, tool.path, tool.flag)
			cancel()
			version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
			if err != nil {
				version = err.Error()
			}
			versions = append(versions, tool.name, version)
		}
		a.logger.Info("media tool versions", versions...)
	})
}

func (a *App) encodeWithFallback(ctx context.Context, id int64, source Source, activity Activity, args []string, input, output string) error {
	nativeErr := a.encode(ctx, id, source.Duration, activity, args)
	if nativeErr == nil || ctx.Err() != nil || !decodingFailure(nativeErr.Error()) || !nativeFLAC(input) {
		return nativeErr
	}
	// Both attempts use the same complete local copy. The independent decoder
	// must reach EOF and pass its CRC/MD5 checks before any artifact is published.
	if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
		return errors.Join(nativeErr, fmt.Errorf("remove failed temporary output: %w", err))
	}
	a.logMediaVersions(ctx)
	a.logger.Warn("trying reference FLAC decoder", "task", a.taskID(id), "job", id, "path", source.Rel, "input_sha256", source.Hash, "detail", nativeErr.Error())
	fallbackArgs := append([]string(nil), args...)
	for i := 0; i+1 < len(fallbackArgs); i++ {
		if fallbackArgs[i] == "-i" {
			fallbackArgs[i+1] = "pipe:0"
			fallbackArgs = append(fallbackArgs[:i], append([]string{"-f", "wav"}, fallbackArgs[i:]...)...)
			break
		}
	}
	err := a.encodeMedia(ctx, id, source.Duration, activity, fallbackArgs, input)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("FLAC fallback did not produce a valid conversion\nNative decoder: %v\nFallback: %w", nativeErr, err)
	}
	a.logger.Info("reference FLAC conversion completed", "task", a.taskID(id), "job", id, "path", source.Rel, "input_sha256", source.Hash)
	return nil
}

func (a *App) logJobFinished(j Job, state string, attempts int, message string, cause error) {
	var activity Activity
	if raw, err := a.meta(taskProgressKey(j.ID)); err == nil {
		_ = json.Unmarshal([]byte(raw), &activity)
	}
	fields := []any{"task", a.taskID(j.ID), "job", j.ID, "kind", j.Kind, "state", state, "attempts", attempts,
		"phase", activity.Phase, "path", activity.Path, "artist", activity.Artist, "album", activity.Album, "title", activity.Title,
		"percent", activity.Percent, "error_kind", failureCategory(message), "detail", message}
	var failure *conversionFailure
	if errors.As(cause, &failure) {
		fields = append(fields, "input_sha256", failure.source.Hash, "source_bytes", failure.source.Size)
	}
	level := slog.LevelInfo
	if state == "failed" && message != "Stopped by administrator" {
		level = slog.LevelError
	} else if state == "pending" && attempts > j.Attempts {
		level = slog.LevelWarn
	}
	a.logger.Log(context.Background(), level, "job finished", fields...)
}
