package forge

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWorkerRecoversCompletionWriteFailureWithoutConsumingExtraAttempts(t *testing.T) {
	a, _ := testApp(t)
	id, err := a.enqueue("fault-probe", "persistence-fault", map[string]bool{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(`CREATE TRIGGER reject_completion BEFORE UPDATE OF state ON jobs WHEN OLD.state='running' AND NEW.state<>'running' BEGIN SELECT RAISE(ABORT,'injected completion write failure'); END`); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	a.logger = slog.New(slog.NewTextHandler(writer, nil))
	observed := make(chan struct{}, 1)
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "job state persistence failed") {
				select {
				case observed <- struct{}{}:
				default:
				}
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.worker(ctx, false, 0) }()
	defer func() {
		cancel()
		<-done
		writer.Close()
		reader.Close()
		<-logDone
	}()
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("failed to inject a completion persistence failure")
	}
	if _, err = a.db.Exec("DROP TRIGGER reject_completion"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var state string
		var attempts int
		if err = a.db.QueryRow("SELECT state,attempts FROM jobs WHERE id=?", id).Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if state == "pending" && attempts == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not recover after writes resumed", state, attempts)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
