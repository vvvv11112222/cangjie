package capture

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vvvv11112222/cangjie/internal/apperror"
)

func isMissing(e error) bool { var a *apperror.Error; return errors.As(e, &a) && a.Code == "NOT_FOUND" }

// checkedFile never traverses symbolic links, junctions or a caller-selected root.
func checkedFile(root, path string) (string, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	rel, e := filepath.Rel(root, abs)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", invalid("recording path escapes capture root")
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", invalid("recording links are not permitted")
		}
		resolved, e := filepath.EvalSymlinks(current)
		if e != nil {
			return "", e
		}
		if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(current)) {
			return "", invalid("recording reparse points are not permitted")
		}
	}
	info, e := os.Stat(abs)
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() {
		return "", invalid("recording must be a regular file")
	}
	return abs, nil
}

func (s *Service) Event(ctx context.Context, v Event) error {
	if len(v.ID) < 1 || len(v.ID) > 128 || v.Path == "" || v.OccurredAt.IsZero() {
		return invalid("invalid stream event")
	}
	if v.Type != "online" && v.Type != "offline" && v.Type != "segment_started" && v.Type != "segment_complete" {
		return invalid("unknown stream event")
	}
	var id string
	if e := s.pool.QueryRow(ctx, `SELECT id::text FROM teaching.live_captures WHERE stream_path=$1`, v.Path).Scan(&id); e != nil {
		return missing()
	}
	tx, r, e := s.lock(ctx, id)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var prior string
	e = tx.QueryRow(ctx, `SELECT request_sha256 FROM teaching.live_capture_events WHERE event_id=$1`, v.ID).Scan(&prior)
	if e == nil {
		if prior != digest(v) {
			return bad("COMPLETION_CONFLICT", "event has different payload")
		}
		return nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	tag, e := tx.Exec(ctx, `INSERT INTO teaching.live_capture_events(event_id,capture_id,request_sha256) VALUES($1,$2,$3) ON CONFLICT(event_id) DO NOTHING`, v.ID, id, digest(v))
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return bad("COMPLETION_CONFLICT", "event identity is already in use")
	}
	// Terminal/invalidated captures acknowledge late events without restoring writes.
	if r.State == "ready" || r.State == "failed" || s.valid(ctx, tx, r) != nil {
		return tx.Commit(ctx)
	}
	if v.Type == "online" {
		// Query authoritative state: a delayed old online hook cannot resurrect a stream.
		p, pe := s.control.Path(ctx, r.Path)
		if pe != nil {
			return pe
		}
		if r.State == "waiting" && r.DisconnectAt == nil && p.Ready && p.Source != nil && p.Source.ID == v.ConnectionID {
			_, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET connection_id=$2,connection_state='connected' WHERE id=$1`, id, v.ConnectionID)
		}
	} else if v.Type == "offline" {
		_, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET connection_state='disconnected',disconnected_at=COALESCE(disconnected_at,now()) WHERE id=$1 AND connection_id=$2`, id, v.ConnectionID)
	} else {
		if v.SegmentPath == nil {
			return invalid("segment_path is required")
		}
		expected := filepath.Join(s.cfg.RecordRoot, filepath.FromSlash(r.Path))
		file, fe := checkedFile(expected, *v.SegmentPath)
		if fe != nil {
			return fe
		}
		key, fe := filepath.Rel(s.cfg.RecordRoot, file)
		if fe != nil {
			return fe
		}
		var size *int64
		var sha *string
		if v.Type == "segment_complete" {
			f, fe := os.Open(file)
			if fe != nil {
				return fe
			}
			info, fe := f.Stat()
			if fe != nil {
				f.Close()
				return fe
			}
			n := info.Size()
			if n <= 0 || n > s.cfg.MaxBytes {
				f.Close()
				return invalid("recording exceeds limits")
			}
			size = &n
			h := newDigest()
			_, fe = io.Copy(h, f)
			f.Close()
			if fe != nil {
				return fe
			}
			d := hexDigest(h)
			sha = &d
		}
		_, e = tx.Exec(ctx, `INSERT INTO teaching.live_capture_segments(capture_id,storage_key,complete,byte_size,sha256) VALUES($1,$2,$3,$4,$5)
   ON CONFLICT(capture_id,storage_key) DO UPDATE SET complete=live_capture_segments.complete OR EXCLUDED.complete,byte_size=COALESCE(EXCLUDED.byte_size,live_capture_segments.byte_size),sha256=COALESCE(EXCLUDED.sha256,live_capture_segments.sha256)`, id, filepath.ToSlash(key), v.Type == "segment_complete", size, sha)
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
