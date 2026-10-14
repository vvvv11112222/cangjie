package governance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	marker := abs + ".initialized"
	_, ledgerErr := os.Stat(abs)
	_, markerErr := os.Stat(marker)
	if ledgerErr != nil && !errors.Is(ledgerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat deletion ledger: %w", ledgerErr)
	}
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat deletion ledger marker: %w", markerErr)
	}
	if errors.Is(ledgerErr, os.ErrNotExist) || errors.Is(markerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("deletion ledger is not fully initialized; run the explicit initialization command before starting the API: %s", abs)
	}
	markerBody, err := os.ReadFile(marker)
	if err != nil {
		return nil, fmt.Errorf("read deletion ledger marker: %w", err)
	}
	if string(markerBody) != "deletion-ledger-v1\n" {
		return nil, fmt.Errorf("deletion ledger marker is invalid: %s", marker)
	}
	file, err := os.OpenFile(abs, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open deletion ledger: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close deletion ledger: %w", err)
	}
	return &Ledger{path: abs}, nil
}

// InitializeLedger is the only operation allowed to create a deletion ledger.
// It must be invoked as an explicit, one-time deployment step; API startup only
// calls OpenLedger and therefore fails closed after a backup restore or storage
// loss where both the ledger and its marker are absent.
func InitializeLedger(path string) (*Ledger, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve deletion ledger path: %w", err)
	}
	marker := abs + ".initialized"
	_, ledgerErr := os.Stat(abs)
	_, markerErr := os.Stat(marker)
	if ledgerErr == nil && markerErr == nil {
		return OpenLedger(abs)
	}
	if ledgerErr != nil && !errors.Is(ledgerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat deletion ledger: %w", ledgerErr)
	}
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat deletion ledger marker: %w", markerErr)
	}
	if ledgerErr == nil || markerErr == nil {
		return nil, fmt.Errorf("refusing to initialize an incomplete deletion ledger; recover the missing durable file: %s", abs)
	}
	dir := filepath.Dir(abs)
	if err = os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create deletion ledger directory: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create deletion ledger: %w", err)
	}
	if err = file.Sync(); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err != nil {
		_ = os.Remove(abs)
		return nil, fmt.Errorf("sync deletion ledger: %w", err)
	}
	markerFile, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = markerFile.WriteString("deletion-ledger-v1\n")
	}
	if err == nil {
		err = markerFile.Sync()
	}
	if markerFile != nil {
		closeErr := markerFile.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = os.Remove(marker)
		_ = os.Remove(abs)
		return nil, fmt.Errorf("create deletion ledger marker: %w", err)
	}
	return OpenLedger(abs)
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
