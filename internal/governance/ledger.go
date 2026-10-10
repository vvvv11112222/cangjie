package governance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Ledger struct {
	path string
	mu   sync.Mutex
}

type Tombstone struct {
	TombstoneID    string    `json:"tombstone_id"`
	SessionID      string    `json:"session_id"`
	RequestedBy    string    `json:"requested_by"`
	Reason         string    `json:"reason"`
	IdempotencyKey string    `json:"idempotency_key"`
	RequestSHA256  string    `json:"request_sha256"`
	RequestedAt    time.Time `json:"requested_at"`
}

func OpenLedger(path string) (*Ledger, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve deletion ledger path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return nil, fmt.Errorf("create deletion ledger directory: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open deletion ledger: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("sync deletion ledger: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close deletion ledger: %w", err)
	}
	return &Ledger{path: abs}, nil
}

func (l *Ledger) Check(ctx context.Context) error {
	_, err := l.ReadAll(ctx)
	return err
}

func (l *Ledger) Append(entry Tombstone) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode deletion tombstone: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open deletion ledger: %w", err)
	}
	defer file.Close()
	if _, err = file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append deletion ledger: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("sync deletion ledger: %w", err)
	}
	return nil
}

func (l *Ledger) ReadAll(ctx context.Context) ([]Tombstone, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.Open(l.path)
	if err != nil {
		return nil, fmt.Errorf("read deletion ledger: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	var entries []Tombstone
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var entry Tombstone
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("deletion ledger line %d is invalid JSON: %w", line, err)
		}
		if entry.TombstoneID == "" || entry.SessionID == "" || entry.RequestedBy == "" || entry.RequestSHA256 == "" || entry.RequestedAt.IsZero() {
			return nil, fmt.Errorf("deletion ledger line %d is missing required fields", line)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan deletion ledger: %w", err)
	}
	return entries, nil
}
