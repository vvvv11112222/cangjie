package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve media root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create media root: %w", err)
	}
	return &Local{root: abs}, nil
}

func (l *Local) Root() string { return l.root }

func (l *Local) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(l.root, ".readiness-*")
	if err != nil {
		return fmt.Errorf("create media readiness file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name) //nolint:errcheck
	if _, err := file.WriteString("ready\n"); err != nil {
		file.Close()
		return fmt.Errorf("write media readiness file: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync media readiness file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close media readiness file: %w", err)
	}
	return nil
}
