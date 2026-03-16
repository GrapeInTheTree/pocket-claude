package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

type Store struct {
	inboxPath   string
	outboxPath  string
	lockPath    string
	lockTimeout time.Duration

	inboxMu  sync.Mutex
	outboxMu sync.Mutex
	logger   *slog.Logger
}

func New(inboxPath, outboxPath, lockPath string, lockTimeout time.Duration, logger *slog.Logger) *Store {
	return &Store{
		inboxPath:   inboxPath,
		outboxPath:  outboxPath,
		lockPath:    lockPath,
		lockTimeout: lockTimeout,
		logger:      logger,
	}
}

// --- Lock file ---

func (s *Store) acquireLock() error {
	if data, err := os.ReadFile(s.lockPath); err == nil {
		var info LockInfo
		if json.Unmarshal(data, &info) == nil {
			lockTime, _ := time.Parse(time.RFC3339, info.Timestamp)
			if time.Since(lockTime) <= s.lockTimeout {
				return fmt.Errorf("lock held by PID %d since %s", info.PID, info.Timestamp)
			}
			s.logger.Warn("Removing stale lock file", "pid", info.PID, "age", time.Since(lockTime).String())
		}
		os.Remove(s.lockPath)
	}

	info := LockInfo{
		PID:       os.Getpid(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal lock info: %w", err)
	}
	return os.WriteFile(s.lockPath, data, 0644)
}

func (s *Store) releaseLock() {
	if err := os.Remove(s.lockPath); err != nil && !os.IsNotExist(err) {
		s.logger.Error("Failed to release lock", "error", err)
	}
}

// --- Inbox ---

func (s *Store) ReadInbox() (InboxFile, error) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	return s.readInbox()
}

func (s *Store) readInbox() (InboxFile, error) {
	var mf InboxFile
	data, err := os.ReadFile(s.inboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return InboxFile{Messages: []InboxMessage{}}, nil
		}
		return mf, err
	}
	if err := json.Unmarshal(data, &mf); err != nil {
		return mf, err
	}
	return mf, nil
}

func (s *Store) writeInbox(mf InboxFile) error {
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.inboxPath, data, 0644)
}

func (s *Store) AppendToInbox(msg InboxMessage) error {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()

	if err := s.acquireLock(); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer s.releaseLock()

	mf, err := s.readInbox()
	if err != nil {
		return fmt.Errorf("read inbox: %w", err)
	}

	mf.Messages = append(mf.Messages, msg)
	return s.writeInbox(mf)
}

func (s *Store) UpdateInbox(fn func(*InboxFile) bool) error {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()

	if err := s.acquireLock(); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer s.releaseLock()

	mf, err := s.readInbox()
	if err != nil {
		return fmt.Errorf("read inbox: %w", err)
	}

	if !fn(&mf) {
		return nil
	}
	return s.writeInbox(mf)
}

func (s *Store) GetInboxStats() (map[string]int, time.Time, error) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()

	mf, err := s.readInbox()
	if err != nil {
		return nil, time.Time{}, err
	}

	stats := make(map[string]int)
	var latest time.Time
	for _, m := range mf.Messages {
		stats[m.Status]++
		if t, err := time.Parse(time.RFC3339, m.Timestamp); err == nil && t.After(latest) {
			latest = t
		}
	}
	return stats, latest, nil
}

func (s *Store) ClearCompleted() (int, error) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()

	if err := s.acquireLock(); err != nil {
		return 0, fmt.Errorf("acquire lock: %w", err)
	}
	defer s.releaseLock()

	mf, err := s.readInbox()
	if err != nil {
		return 0, err
	}

	var kept []InboxMessage
	removed := 0
	for _, m := range mf.Messages {
		switch m.Status {
		case StatusDone, StatusSent, StatusFailed, StatusExpired:
			removed++
		default:
			kept = append(kept, m)
		}
	}

	if removed > 0 {
		mf.Messages = kept
		if err := s.writeInbox(mf); err != nil {
			return 0, err
		}
	}
	return removed, nil
}

// --- Outbox ---

func (s *Store) readOutbox() (OutboxFile, error) {
	data, err := os.ReadFile(s.outboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return OutboxFile{Messages: []OutboxMessage{}}, nil
		}
		return OutboxFile{}, err
	}

	var mf OutboxFile
	if err := json.Unmarshal(data, &mf); err == nil {
		return mf, nil
	}

	// Flexible parsing: handle array format from legacy Cowork
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return OutboxFile{}, fmt.Errorf("outbox parse error: %w", err)
	}

	var messages []OutboxMessage
	for _, item := range raw {
		var nested OutboxFile
		if json.Unmarshal(item, &nested) == nil && len(nested.Messages) > 0 {
			messages = append(messages, nested.Messages...)
			continue
		}
		var msg OutboxMessage
		if json.Unmarshal(item, &msg) == nil && msg.ID != "" {
			messages = append(messages, msg)
		}
	}

	s.logger.Warn("Outbox had non-standard format, normalized", "raw_items", len(raw), "messages", len(messages))
	return OutboxFile{Messages: messages}, nil
}

func (s *Store) writeOutbox(mf OutboxFile) error {
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.outboxPath, data, 0644)
}

func (s *Store) UpdateOutbox(fn func(*OutboxFile) bool) error {
	s.outboxMu.Lock()
	defer s.outboxMu.Unlock()

	mf, err := s.readOutbox()
	if err != nil {
		return fmt.Errorf("read outbox: %w", err)
	}

	if !fn(&mf) {
		return nil
	}
	return s.writeOutbox(mf)
}
