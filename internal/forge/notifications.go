package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type notificationState struct {
	notifyMu     sync.Mutex
	notifyNext   time.Time
	offlineSince time.Time
}

type notificationDelivery struct {
	Version     int    `json:"version"`
	Endpoint    string `json:"endpoint"`
	FailedID    int64  `json:"failed_id"`
	FailedAt    int64  `json:"failed_at,omitempty"`
	CompletedID int64  `json:"completed_id"`
	CompletedAt int64  `json:"completed_at"`
	Offline     bool   `json:"offline"`
	OfflineAt   int64  `json:"offline_at,omitempty"`
	LowDisk     bool   `json:"low_disk"`
}

func sendNotification(ctx context.Context, options maintenanceOptions, event, message string) error {
	if options.NotifyURL == "" {
		return errors.New("Configure a notification endpoint first")
	}
	if err := options.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var data []byte
	contentType := "application/json"
	if options.NotifyMode == "ntfy" {
		data, contentType = []byte(message), "text/plain; charset=utf-8"
	} else {
		data, _ = json.Marshal(map[string]any{"application": "MusicForge", "event": event, "message": message, "time": time.Now().Unix()})
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, options.NotifyURL, bytes.NewReader(data))
	if err != nil {
		return errors.New("Invalid notification endpoint")
	}
	request.Header.Set("Content-Type", contentType)
	if options.NotifyMode == "ntfy" {
		request.Header.Set("Title", "MusicForge")
	}
	if options.NotifyToken != "" {
		request.Header.Set("Authorization", "Bearer "+options.NotifyToken)
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Notification delivery failed; check the endpoint and connection")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Notification endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (a *App) testNotification(w http.ResponseWriter, r *http.Request) {
	options, err := a.maintenanceOptions(r.Context())
	if err == nil {
		err = sendNotification(r.Context(), options, "test", "MusicForge notification connection is working.")
	}
	if err != nil {
		apiError(w, 502, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

// Scheduler-only, bounded and independent of audio jobs. Persist only delivery
// cursors, so restarts do not repeat alerts or expose endpoint credentials.
func (a *App) notificationTick(ctx context.Context, s Settings) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()
	now := time.Now()
	options, err := a.maintenanceOptions(ctx)
	if err != nil || options.NotifyURL == "" {
		return
	}
	online, _, checkedAt := a.storageStatus(s)
	if checkedAt != 0 {
		if online {
			a.offlineSince = time.Time{}
		} else if a.offlineSince.IsZero() {
			a.offlineSince = time.Unix(checkedAt, 0)
		}
	}
	if now.Before(a.notifyNext) {
		return
	}
	a.notifyNext = now.Add(time.Minute)
	state := notificationDelivery{Version: 1, Endpoint: digest(options.NotifyURL + "\x00" + options.NotifyMode + "\x00" + options.NotifyToken)}
	raw, err := a.metaContext(ctx, "maintenance:notifications")
	if err == nil {
		var saved notificationDelivery
		if json.Unmarshal([]byte(raw), &saved) == nil && saved.Version == 1 && saved.Endpoint == state.Endpoint {
			state = saved
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	defer func() {
		encoded, _ := json.Marshal(state)
		if err := a.setMeta("maintenance:notifications", string(encoded)); err != nil {
			a.logger.Warn("notification cursor save failed", "error", err)
		}
	}()
	deliver := func(event, message string) bool {
		if err := sendNotification(ctx, options, event, message); err != nil {
			a.notifyNext = now.Add(5 * time.Minute)
			a.logger.Warn("notification delivery failed", "event", event, "error", err)
			return false
		}
		return true
	}
	var failedID, failedAt int64
	var failures int
	if err = a.reads.QueryRowContext(ctx, "SELECT coalesce(max(id),0),coalesce(max(updated),0),count(*) FROM jobs WHERE state='failed' AND attempts>=3").Scan(&failedID, &failedAt, &failures); err != nil {
		return
	}
	if failures > 0 && (failedID > state.FailedID || failedAt > state.FailedAt) {
		if !deliver("failed_jobs", fmt.Sprintf("%d track jobs exhausted their retries. Open MusicForge Jobs to review them.", failures)) {
			return
		}
		state.FailedID, state.FailedAt = max(state.FailedID, failedID), max(state.FailedAt, failedAt)
	}
	if checkedAt != 0 {
		if online {
			state.Offline = false
		} else {
			if state.OfflineAt != a.offlineSince.Unix() {
				state.Offline = false
			}
			if !state.Offline && now.Sub(a.offlineSince) >= 5*time.Minute {
				if !deliver("storage_offline", "The last observed MusicForge storage failure has not been cleared for at least five minutes. Check the mounts and library settings.") {
					return
				}
				state.Offline = true
				state.OfflineAt = a.offlineSince.Unix()
			}
		}
	}
	a.storageMu.Lock()
	low := a.storageLow
	if a.storageSource != s.Source || a.storageOutput != s.Output {
		low = nil
	}
	a.storageMu.Unlock()
	if low != nil {
		if *low && !state.LowDisk {
			if !deliver("low_disk", "MusicForge configuration or output storage has less than 256 MiB available.") {
				return
			}
		}
		state.LowDisk = *low
	}
	if options.NotifyCompletion {
		tasks, _, taskErr := a.taskListContext(ctx, "success", 25, 0)
		if taskErr != nil {
			return
		}
		var latest *Task
		for i := range tasks {
			task := &tasks[i]
			if task.Counts.Total > 0 && (latest == nil || task.Updated > latest.Updated || task.Updated == latest.Updated && task.ID > latest.ID) {
				latest = task
			}
		}
		if latest != nil && (latest.Updated > state.CompletedAt || latest.Updated == state.CompletedAt && latest.ID > state.CompletedID) {
			if !deliver("batch_complete", fmt.Sprintf("MusicForge task #%d completed: %d tracks ready.", latest.ID, latest.Counts.Done)) {
				return
			}
			state.CompletedID, state.CompletedAt = latest.ID, latest.Updated
		}
	}
}
