package store

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	st := New(dir+"/inbox.json", dir+"/outbox.json", dir+"/inbox.lock", 5*time.Minute, logger)
	return st, dir
}

func TestAppendAndReadInbox(t *testing.T) {
	st, _ := testStore(t)

	msg := InboxMessage{
		ID:        "msg_001",
		Text:      "hello",
		Status:    StatusPending,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	if err := st.AppendToInbox(msg); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}

	inbox, err := st.ReadInbox()
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}

	if len(inbox.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(inbox.Messages))
	}
	if inbox.Messages[0].ID != "msg_001" {
		t.Errorf("ID = %q, want 'msg_001'", inbox.Messages[0].ID)
	}
	if inbox.Messages[0].Text != "hello" {
		t.Errorf("Text = %q, want 'hello'", inbox.Messages[0].Text)
	}
}

func TestAppendMultiple(t *testing.T) {
	st, _ := testStore(t)

	for i := 0; i < 5; i++ {
		msg := InboxMessage{
			ID:        "msg_" + string(rune('A'+i)),
			Text:      "text",
			Status:    StatusPending,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := st.AppendToInbox(msg); err != nil {
			t.Fatalf("AppendToInbox #%d: %v", i, err)
		}
	}

	inbox, err := st.ReadInbox()
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(inbox.Messages) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(inbox.Messages))
	}
}

func TestUpdateInbox(t *testing.T) {
	st, _ := testStore(t)

	msg := InboxMessage{
		ID:        "msg_upd",
		Text:      "original",
		Status:    StatusPending,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	st.AppendToInbox(msg)

	// Update status
	st.UpdateInbox(func(mf *InboxFile) bool {
		for i := range mf.Messages {
			if mf.Messages[i].ID == "msg_upd" {
				mf.Messages[i].Status = StatusProcessing
				return true
			}
		}
		return false
	})

	inbox, _ := st.ReadInbox()
	if inbox.Messages[0].Status != StatusProcessing {
		t.Errorf("Status = %q, want 'processing'", inbox.Messages[0].Status)
	}
}

func TestUpdateInboxNoChange(t *testing.T) {
	st, _ := testStore(t)

	msg := InboxMessage{
		ID:        "msg_nochange",
		Text:      "text",
		Status:    StatusPending,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	st.AppendToInbox(msg)

	// Return false = no write
	st.UpdateInbox(func(mf *InboxFile) bool {
		return false
	})

	// Should still be readable
	inbox, err := st.ReadInbox()
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(inbox.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(inbox.Messages))
	}
}

func TestClearCompleted(t *testing.T) {
	st, _ := testStore(t)

	statuses := []string{StatusPending, StatusDone, StatusSent, StatusFailed, StatusExpired, StatusError, StatusProcessing}
	for i, s := range statuses {
		msg := InboxMessage{
			ID:        "msg_" + string(rune('A'+i)),
			Status:    s,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		st.AppendToInbox(msg)
	}

	removed, err := st.ClearCompleted()
	if err != nil {
		t.Fatalf("ClearCompleted: %v", err)
	}

	// done, sent, failed, expired = 4 removed
	if removed != 4 {
		t.Errorf("Removed = %d, want 4", removed)
	}

	inbox, _ := st.ReadInbox()
	// pending, error, processing = 3 remaining
	if len(inbox.Messages) != 3 {
		t.Errorf("Remaining = %d, want 3", len(inbox.Messages))
	}
}

func TestGetInboxStats(t *testing.T) {
	st, _ := testStore(t)

	st.AppendToInbox(InboxMessage{ID: "1", Status: StatusPending, Timestamp: "2024-01-01T00:00:00Z"})
	st.AppendToInbox(InboxMessage{ID: "2", Status: StatusPending, Timestamp: "2024-01-02T00:00:00Z"})
	st.AppendToInbox(InboxMessage{ID: "3", Status: StatusDone, Timestamp: "2024-01-03T00:00:00Z"})

	stats, latest, err := st.GetInboxStats()
	if err != nil {
		t.Fatalf("GetInboxStats: %v", err)
	}
	if stats[StatusPending] != 2 {
		t.Errorf("pending = %d, want 2", stats[StatusPending])
	}
	if stats[StatusDone] != 1 {
		t.Errorf("done = %d, want 1", stats[StatusDone])
	}

	expected, _ := time.Parse(time.RFC3339, "2024-01-03T00:00:00Z")
	if !latest.Equal(expected) {
		t.Errorf("latest = %v, want %v", latest, expected)
	}
}

func TestReadEmptyInbox(t *testing.T) {
	st, _ := testStore(t)

	inbox, err := st.ReadInbox()
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(inbox.Messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(inbox.Messages))
	}
}

func TestInboxMessageAge(t *testing.T) {
	past := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	msg := InboxMessage{Timestamp: past}

	age := msg.Age()
	if age < 4*time.Minute || age > 6*time.Minute {
		t.Errorf("Age = %v, expected ~5 minutes", age)
	}
}

func TestInboxMessageAgeInvalidTimestamp(t *testing.T) {
	msg := InboxMessage{Timestamp: "not-a-date"}
	if msg.Age() != 0 {
		t.Error("Expected 0 for invalid timestamp")
	}
}

func TestUpdateOutbox(t *testing.T) {
	st, _ := testStore(t)

	// Add message to outbox
	st.UpdateOutbox(func(mf *OutboxFile) bool {
		mf.Messages = append(mf.Messages, OutboxMessage{
			ID:     "out_1",
			Status: StatusDone,
			Result: "hello",
		})
		return true
	})

	// Update message status
	st.UpdateOutbox(func(mf *OutboxFile) bool {
		for i := range mf.Messages {
			if mf.Messages[i].ID == "out_1" {
				mf.Messages[i].Status = StatusSent
				return true
			}
		}
		return false
	})

	// Verify
	var final OutboxFile
	st.UpdateOutbox(func(mf *OutboxFile) bool {
		final = *mf
		return false
	})

	if len(final.Messages) != 1 {
		t.Fatalf("Expected 1 outbox message, got %d", len(final.Messages))
	}
	if final.Messages[0].Status != StatusSent {
		t.Errorf("Status = %q, want 'sent'", final.Messages[0].Status)
	}
}
