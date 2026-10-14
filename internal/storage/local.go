package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrTooLarge = errors.New("object exceeds configured size limit")
	ErrEmpty    = errors.New("object is empty")
)

type StagedObject struct {
	path   string
	SHA256 string
	Size   int64
	Header []byte
}

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type Backend interface {
	Check(context.Context) error
	Stage(context.Context, io.Reader, int64) (*StagedObject, error)
	Commit(*StagedObject, string) error
	Discard(*StagedObject) error
	Open(string) (ReadSeekCloser, error)
	Remove(string) error
}

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

func (l *Local) Stage(ctx context.Context, reader io.Reader, maxBytes int64) (*StagedObject, error) {
	staging := filepath.Join(l.root, ".staging")
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return nil, fmt.Errorf("create media staging directory: %w", err)
	}
	file, err := os.CreateTemp(staging, "upload-*")
	if err != nil {
		return nil, fmt.Errorf("create staged media: %w", err)
	}
	name := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	hash := sha256.New()
	prefix := &prefixWriter{limit: 512}
	written, err := io.Copy(io.MultiWriter(file, hash, prefix), &contextReader{ctx: ctx, reader: io.LimitReader(reader, maxBytes+1)})
	if err != nil {
		return nil, fmt.Errorf("write staged media: %w", err)
	}
	if written > maxBytes {
		return nil, ErrTooLarge
	}
	if written == 0 {
		return nil, ErrEmpty
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync staged media: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close staged media: %w", err)
	}
	ok = true
	return &StagedObject{path: name, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: written, Header: prefix.data}, nil
}

func (l *Local) Commit(staged *StagedObject, objectKey string) error {
	destination, err := l.resolve(objectKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create media object directory: %w", err)
	}
	if err := os.Rename(staged.path, destination); err != nil {
		return fmt.Errorf("commit media object: %w", err)
	}
	staged.path = ""
	return nil
}

func (l *Local) Discard(staged *StagedObject) error {
	if staged == nil || staged.path == "" {
		return nil
	}
	err := os.Remove(staged.path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	staged.path = ""
	return err
}

func (l *Local) Open(objectKey string) (ReadSeekCloser, error) {
	path, err := l.resolve(objectKey)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (l *Local) Remove(objectKey string) error {
	path, err := l.resolve(objectKey)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l *Local) resolve(objectKey string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(objectKey))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid media object key")
	}
	path := filepath.Join(l.root, clean)
	relative, err := filepath.Rel(l.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("media object key escapes storage root")
	}
	return path, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

type prefixWriter struct {
	limit int
	data  []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	remaining := w.limit - len(w.data)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		w.data = append(w.data, p[:remaining]...)
	}
	return len(p), nil
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

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
