package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestBackgroundPoolEmptyStatus(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	if got := pool.Status(); got != "No background tasks." {
		t.Errorf("Expected empty status, got %q", got)
	}
	if got := pool.RunningCount(); got != 0 {
		t.Errorf("Expected 0 running, got %d", got)
	}
}

func TestBackgroundPoolSlotLimit(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	// Fill all 3 semaphore slots manually
	pool.sem <- struct{}{}
	pool.sem <- struct{}{}
	pool.sem <- struct{}{}

	_, err := pool.Submit(context.Background(), "test", "hello")
	if err == nil || !strings.Contains(err.Error(), "slots are busy") {
		t.Errorf("Expected slots busy error, got %v", err)
	}

	<-pool.sem
	<-pool.sem
	<-pool.sem
}

func TestBackgroundPoolCancelAll(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	pool.mu.Lock()
	pool.tasks["bg_1"] = &BackgroundTask{ID: "bg_1", State: "running", Cancel: cancel1, StartedAt: time.Now()}
	pool.tasks["bg_2"] = &BackgroundTask{ID: "bg_2", State: "approval", Cancel: cancel2, StartedAt: time.Now()}
	pool.tasks["bg_3"] = &BackgroundTask{ID: "bg_3", State: "done", Cancel: func() {}, StartedAt: time.Now()}
	pool.mu.Unlock()

	pool.CancelAll()

	pool.mu.Lock()
	defer pool.mu.Unlock()

	if pool.tasks["bg_1"].State != "cancelled" {
		t.Errorf("bg_1 state = %q, want 'cancelled'", pool.tasks["bg_1"].State)
	}
	if pool.tasks["bg_2"].State != "cancelled" {
		t.Errorf("bg_2 state = %q, want 'cancelled'", pool.tasks["bg_2"].State)
	}
	if pool.tasks["bg_3"].State != "done" {
		t.Errorf("bg_3 state = %q, want 'done' (should be unchanged)", pool.tasks["bg_3"].State)
	}
	if !pool.closed {
		t.Error("Expected pool to be closed after CancelAll")
	}
	if ctx1.Err() == nil {
		t.Error("Expected bg_1 context to be cancelled")
	}
	if ctx2.Err() == nil {
		t.Error("Expected bg_2 context to be cancelled")
	}
}

func TestBackgroundPoolCancelSpecific(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	_, cancel := context.WithCancel(context.Background())
	pool.mu.Lock()
	pool.tasks["bg_1"] = &BackgroundTask{ID: "bg_1", State: "running", Cancel: cancel, StartedAt: time.Now()}
	pool.tasks["bg_2"] = &BackgroundTask{ID: "bg_2", State: "done", Cancel: func() {}, StartedAt: time.Now()}
	pool.mu.Unlock()

	// Cancel running task
	if err := pool.Cancel("bg_1"); err != nil {
		t.Errorf("Cancel bg_1: %v", err)
	}
	pool.mu.Lock()
	if pool.tasks["bg_1"].State != "cancelled" {
		t.Errorf("bg_1 state = %q, want 'cancelled'", pool.tasks["bg_1"].State)
	}
	pool.mu.Unlock()

	// Cancel already-done task should error
	if err := pool.Cancel("bg_2"); err == nil {
		t.Error("Expected error cancelling done task")
	}

	// Cancel nonexistent task
	if err := pool.Cancel("bg_999"); err == nil {
		t.Error("Expected error cancelling nonexistent task")
	}
}

func TestBackgroundPoolCleanup(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	old := time.Now().Add(-1 * time.Hour)
	recent := time.Now().Add(-5 * time.Minute)

	pool.mu.Lock()
	pool.tasks["old_done"] = &BackgroundTask{State: "done", StartedAt: old}
	pool.tasks["old_failed"] = &BackgroundTask{State: "failed", StartedAt: old}
	pool.tasks["old_cancelled"] = &BackgroundTask{State: "cancelled", StartedAt: old}
	pool.tasks["recent_done"] = &BackgroundTask{State: "done", StartedAt: recent}
	pool.tasks["still_running"] = &BackgroundTask{State: "running", StartedAt: old}
	pool.mu.Unlock()

	pool.Cleanup(30 * time.Minute)

	pool.mu.Lock()
	defer pool.mu.Unlock()

	for _, id := range []string{"old_done", "old_failed", "old_cancelled"} {
		if _, ok := pool.tasks[id]; ok {
			t.Errorf("Expected %s to be cleaned up", id)
		}
	}
	for _, id := range []string{"recent_done", "still_running"} {
		if _, ok := pool.tasks[id]; !ok {
			t.Errorf("Expected %s to remain", id)
		}
	}
}

func TestBackgroundPoolClosedRejectsSubmit(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	pool.CancelAll()

	_, err := pool.Submit(context.Background(), "test", "hello")
	if err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Errorf("Expected shutting down error, got %v", err)
	}
}

func TestBackgroundPoolResolveApproval(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	ch := make(chan bool, 1)
	pool.approvals.Store("bg_test", ch)

	pool.ResolveApproval("bg_test", true)

	select {
	case got := <-ch:
		if !got {
			t.Error("Expected true approval")
		}
	default:
		t.Error("Expected approval to be resolved")
	}

	// Resolve nonexistent should not panic
	pool.ResolveApproval("bg_nonexistent", false)
}

func TestBackgroundPoolResolveApprovalDuplicate(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	ch := make(chan bool, 1)
	pool.approvals.Store("bg_dup", ch)

	// First resolve succeeds
	pool.ResolveApproval("bg_dup", true)
	// Second resolve should not block (buffered channel, default case)
	pool.ResolveApproval("bg_dup", false)

	got := <-ch
	if !got {
		t.Error("Expected first resolve value (true)")
	}
}

func TestBackgroundPoolStatus(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	pool.mu.Lock()
	pool.tasks["bg_1"] = &BackgroundTask{
		ID: "bg_1", State: "running", Project: "my-app",
		Message: "analyze code", Cancel: func() {}, StartedAt: time.Now(),
	}
	pool.tasks["bg_2"] = &BackgroundTask{
		ID: "bg_2", State: "approval", Project: "api-server",
		Message: "run tests", Cancel: func() {}, StartedAt: time.Now(),
	}
	pool.tasks["bg_3"] = &BackgroundTask{
		ID: "bg_3", State: "done", Project: "old",
		Message: "finished", Cancel: func() {}, StartedAt: time.Now(),
	}
	pool.mu.Unlock()

	status := pool.Status()

	if !strings.Contains(status, "bg_1") || !strings.Contains(status, "my-app") {
		t.Error("Expected bg_1 and my-app in status")
	}
	if !strings.Contains(status, "bg_2") || !strings.Contains(status, "api-server") {
		t.Error("Expected bg_2 and api-server in status")
	}
	if strings.Contains(status, "bg_3") {
		t.Error("Done task should not appear in status")
	}
	if !strings.Contains(status, "Slots: 2/3") {
		t.Errorf("Expected 'Slots: 2/3', got %q", status)
	}
}

func TestBackgroundPoolStatusNoActive(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	pool.mu.Lock()
	pool.tasks["bg_done"] = &BackgroundTask{State: "done", StartedAt: time.Now()}
	pool.mu.Unlock()

	if got := pool.Status(); got != "No active background tasks." {
		t.Errorf("Expected no active status, got %q", got)
	}
}

func TestBackgroundPoolRunningCount(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	pool.mu.Lock()
	pool.tasks["bg_1"] = &BackgroundTask{State: "running"}
	pool.tasks["bg_2"] = &BackgroundTask{State: "approval"}
	pool.tasks["bg_3"] = &BackgroundTask{State: "done"}
	pool.tasks["bg_4"] = &BackgroundTask{State: "failed"}
	pool.tasks["bg_5"] = &BackgroundTask{State: "cancelled"}
	pool.mu.Unlock()

	if got := pool.RunningCount(); got != 2 {
		t.Errorf("RunningCount() = %d, want 2", got)
	}
}

func TestTaskCounterUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("bg_%d", taskCounter.Add(1))
		if seen[id] {
			t.Fatalf("Duplicate task ID: %s", id)
		}
		seen[id] = true
	}
}

func TestTaskCounterConcurrency(t *testing.T) {
	seen := sync.Map{}
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				id := fmt.Sprintf("bg_%d", taskCounter.Add(1))
				if _, loaded := seen.LoadOrStore(id, true); loaded {
					t.Errorf("Duplicate ID from concurrent access: %s", id)
				}
			}
		}()
	}
	wg.Wait()
}
