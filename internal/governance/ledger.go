package governance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Ledger struct {
	path string
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
	file, err := os.Open(l.path)
	if err != nil {
		return fmt.Errorf("read deletion ledger: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return err
		}
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return fmt.Errorf("deletion ledger line %d is invalid JSON: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan deletion ledger: %w", err)
	}
	return nil
}
