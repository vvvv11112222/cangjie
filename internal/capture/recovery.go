package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	stdhash "hash"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func newDigest() stdhash.Hash         { return sha256.New() }
func hexDigest(h stdhash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

func (s *Service) tick(ctx context.Context) {
	rows, e := s.pool.Query(ctx, `SELECT id::text FROM teaching.live_captures WHERE state IN('waiting','recording','finalizing') ORDER BY created_at LIMIT 100`)
	if e != nil {
		slog.Error("capture scan failed", "error", e)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if e = s.reconcile(ctx, id); e != nil {
			slog.Warn("capture reconciliation pending", "capture_id", id, "error", e)
		}
	}
	if e = s.expireConnections(ctx); e != nil {
		slog.Warn("capture connection recheck pending", "error", e)
	}
	// Terminal captures may still have connections/files when a previous cleanup failed.
	rows, e = s.pool.Query(ctx, `SELECT id::text FROM teaching.live_captures WHERE state IN('failed','ready') AND recording_state<>'cleaned' LIMIT 100`)
	if e != nil {
		return
	}
	ids = nil
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if r, le := load(ctx, s.pool, id, false); le == nil {
			if ce := s.clean(ctx, r); ce != nil {
				slog.Warn("capture cleanup pending", "capture_id", id, "error", ce)
			}
		}
	}
}
func (s *Service) reconcile(ctx context.Context, id string) error {
	r, e := load(ctx, s.pool, id, false)
	if e != nil {
		return e
	}
	if (r.State == "waiting" || r.State == "recording") && !r.ExpiresAt.After(time.Now()) {
		return s.requestStop(ctx, r, "maximum_duration")
	}
	p, e := s.identity.PrincipalByID(ctx, r.UserID)
	if e != nil || s.valid(ctx, s.pool, r) != nil || s.media.AuthorizeUpload(ctx, p, r.SessionID) != nil {
		return s.abort(ctx, r, "SOURCE_NOT_VERIFIED")
	}
	if r.State == "waiting" && time.Since(r.CreatedAt) > s.cfg.WaitingTimeout {
		return s.requestStop(ctx, r, "waiting_timeout")
	}
	path, e := s.control.Path(ctx, r.Path)
	if e != nil && !isMissing(e) {
		return e
	}
	online := e == nil && path.Ready
	if online && path.Source != nil && r.DisconnectAt == nil && r.StopAt == nil {
		_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET connection_state='connected',connection_id=$2 WHERE id=$1 AND state IN('waiting','recording') AND stop_requested_at IS NULL AND disconnected_at IS NULL`, id, path.Source.ID)
		if e != nil {
			return e
		}
		// A nonempty server-created recording proves the recorder has begun writing.
		files, fileErr := s.files(r)
		if fileErr != nil {
			return s.requestStop(ctx, r, "recording_limit_or_storage_error")
		}
		if len(files) > 0 {
			info, statErr := os.Stat(files[len(files)-1])
			if statErr != nil {
				return statErr
			}
			if time.Since(info.ModTime()) > 30*time.Second {
				return s.requestStop(ctx, r, "recorder_stalled")
			}
			_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET state='recording',recording_state='recording',started_at=COALESCE(started_at,now()),audio_track=$2,video_track=$3 WHERE id=$1 AND state IN('waiting','recording') AND stop_requested_at IS NULL AND disconnected_at IS NULL`, id, track(path.Tracks, false), track(path.Tracks, true))
			if e != nil {
				return e
			}
		}
	} else if !online && r.ConnectionState == "connected" && r.StopAt == nil {
		_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET connection_state='disconnected',disconnected_at=COALESCE(disconnected_at,now()) WHERE id=$1`, id)
		if e != nil {
			return e
		}
	}
	if r.DisconnectAt != nil && time.Since(*r.DisconnectAt) > s.cfg.DisconnectTimeout && r.StopAt == nil {
		return s.requestStop(ctx, r, "disconnected")
	}
	if r.State != "finalizing" {
		return nil
	}
	if r.StopAt != nil && time.Since(*r.StopAt) > s.cfg.FinalizeTimeout {
		return s.abort(ctx, r, "CAPTURE_FINALIZE_TIMEOUT")
	}
	if e = s.control.ClosePath(ctx, r.Path); e != nil {
		return e
	}
	path, e = s.control.Path(ctx, r.Path)
	if e != nil && !isMissing(e) {
		return e
	}
	if e == nil && path.Ready {
		return nil
	}
	// Give the recorder time to close its final part before recovering missed hooks.
	if r.StopAt == nil || time.Since(*r.StopAt) < 3*time.Second {
		return nil
	}
	if r.MediaID != nil {
		return s.prepare(ctx, r, p)
	}
	token, e := identity.RandomToken()
	if e != nil {
		return e
	}
	token = token[:8] + "-" + token[8:12] + "-4" + token[13:16] + "-a" + token[17:20] + "-" + token[20:32]
	tag, e := s.pool.Exec(ctx, `UPDATE teaching.live_captures SET finalize_token=$2::uuid,finalize_until=now()+interval '30 seconds',attempts=attempts+1 WHERE id=$1 AND state='finalizing' AND media_asset_id IS NULL AND available_at<=now() AND (finalize_until IS NULL OR finalize_until<now()) AND attempts<3`, id, token)
	if e != nil || tag.RowsAffected() == 0 {
		return e
	}
	work, cancel := context.WithTimeout(ctx, s.cfg.FinalizeTimeout)
	defer cancel()
	e = s.finalize(work, r, p, token)
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer releaseCancel()
	if e != nil {
		_, releaseErr := s.pool.Exec(releaseCtx, `UPDATE teaching.live_captures SET finalize_token=NULL,finalize_until=NULL,available_at=now()+interval '10 seconds',error_code=CASE WHEN state='finalizing' THEN 'CAPTURE_FINALIZE_FAILED' ELSE error_code END WHERE id=$1 AND finalize_token=$2::uuid`, id, token)
		if releaseErr != nil {
			return errors.Join(e, releaseErr)
		}
		var attempts int
		var state string
		_ = s.pool.QueryRow(releaseCtx, `SELECT attempts,state FROM teaching.live_captures WHERE id=$1`, id).Scan(&attempts, &state)
		if attempts >= 3 && state == "finalizing" {
			return errors.Join(e, s.abort(ctx, r, "CAPTURE_FINALIZE_FAILED"))
		}
	} else {
		_, e = s.pool.Exec(releaseCtx, `UPDATE teaching.live_captures SET finalize_token=NULL,finalize_until=NULL WHERE id=$1 AND finalize_token=$2::uuid`, id, token)
	}
	return e
}
func track(names []string, video bool) string {
	for _, n := range names {
		n = strings.ToUpper(n)
		v := strings.Contains(n, "H264") || strings.Contains(n, "H265") || strings.Contains(n, "VP") || strings.Contains(n, "AV1")
		if v == video {
			return "present"
		}
	}
	return "unknown"
}
func (s *Service) requestStop(ctx context.Context, r record, reason string) error {
	tx, v, e := s.lock(ctx, r.ID)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if v.State != "waiting" && v.State != "recording" {
		return nil
	}
	if _, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET state='finalizing',stop_requested_at=now(),recording_state='stopping',end_reason=$2 WHERE id=$1`, r.ID, reason); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE teaching.capture_grants SET revoked_at=now() WHERE capture_id=$1 AND purpose IN('publish','read')`, r.ID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) abort(ctx context.Context, r record, code string) error {
	tx, _, e := s.lock(ctx, r.ID)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET state='failed',error_code=$2,end_reason=COALESCE(end_reason,'invalidated'),ended_at=now(),recording_state='cleanup_pending' WHERE id=$1`, r.ID, code); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE teaching.capture_grants SET revoked_at=now() WHERE capture_id=$1`, r.ID); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	return s.clean(ctx, r)
}
func (s *Service) files(r record) ([]string, error) {
	dir := filepath.Join(s.cfg.RecordRoot, filepath.FromSlash(r.Path))
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if len(entries) > 1024 {
		return nil, invalid("too many recording segments")
	}
	files := []string{}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		file, e := checkedFile(s.cfg.RecordRoot, filepath.Join(dir, entry.Name()))
		if e != nil {
			return nil, e
		}
		info, e := os.Stat(file)
		if e != nil {
			return nil, e
		}
		total += info.Size()
		if total > s.cfg.MaxBytes {
			return nil, invalid("recording exceeds byte limit")
		}
		if info.Size() > 0 {
			files = append(files, file)
		}
	}
	return files, nil
}
func (s *Service) clean(ctx context.Context, r record) error {
	if e := s.control.ClosePath(ctx, r.Path); e != nil {
		return e
	}
	p, e := s.control.Path(ctx, r.Path)
	if e != nil && !isMissing(e) {
		return e
	}
	if e == nil && p.Ready {
		return unavailable()
	}
	// Never remove a directory while a finalizer may still hold an input/output.
	var active bool
	if e = s.pool.QueryRow(ctx, `SELECT finalize_until>now() FROM teaching.live_captures WHERE id=$1 AND finalize_until IS NOT NULL`, r.ID).Scan(&active); e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if active {
		return bad("INVALID_STATE", "finalizer is still active")
	}
	target := filepath.Join(s.cfg.RecordRoot, filepath.FromSlash(r.Path))
	rel, relErr := filepath.Rel(s.cfg.RecordRoot, target)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return invalid("unsafe capture cleanup target")
	}
	if _, e = os.Stat(target); e == nil {
		// Validate every path before the recursive removal (including Windows junctions).
		resolvedRoot, resolveErr := filepath.EvalSymlinks(s.cfg.RecordRoot)
		if resolveErr != nil {
			return resolveErr
		}
		e = filepath.WalkDir(target, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			resolved, e := filepath.EvalSymlinks(path)
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(s.cfg.RecordRoot, path)
			expected := filepath.Join(resolvedRoot, rel)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || d.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(expected)) {
				return invalid("unsafe recording cleanup path")
			}
			return nil
		})
		if e != nil {
			return e
		}
		if e = os.RemoveAll(target); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	entries, e := os.ReadDir(s.cfg.RecordRoot)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".finalize-"+r.ID+"-") {
			work := filepath.Join(s.cfg.RecordRoot, entry.Name())
			resolved, er := filepath.EvalSymlinks(work)
			if er != nil {
				return er
			}
			resolvedRoot, er := filepath.EvalSymlinks(s.cfg.RecordRoot)
			if er != nil {
				return er
			}
			expected := filepath.Join(resolvedRoot, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(expected)) {
				return invalid("unsafe finalizer directory")
			}
			files, er := os.ReadDir(work)
			if er != nil {
				return er
			}
			for _, f := range files {
				if f.IsDir() {
					return invalid("unexpected finalizer subdirectory")
				}
				if er = os.Remove(filepath.Join(work, f.Name())); er != nil {
					return er
				}
			}
			if er = os.Remove(work); er != nil {
				return er
			}
		}
	}
	_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET recording_state='cleaned' WHERE id=$1`, r.ID)
	return e
}

// CleanupSession blocks governance completion until media connections and files
// are closed, and an in-flight finalizer has acknowledged its bounded lease.
func (s *Service) CleanupSession(ctx context.Context, session string) error {
	rows, e := s.pool.Query(ctx, `SELECT id::text FROM teaching.live_captures WHERE session_id=$1`, session)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		r, e := load(ctx, s.pool, id, false)
		if e != nil {
			return e
		}
		// Clear the state first, preserving the active lease until its process exits.
		_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET state='failed',end_reason='deleted',error_code='RESOURCE_EXPIRED' WHERE id=$1`, id)
		if e != nil {
			return e
		}
		_, e = s.pool.Exec(ctx, `UPDATE teaching.capture_grants SET revoked_at=now() WHERE capture_id=$1`, id)
		if e != nil {
			return e
		}
		if e = s.clean(ctx, r); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) expireConnections(ctx context.Context) error {
	rows, e := s.pool.Query(ctx, `SELECT n.connection_id::text,c.id::text,g.user_id::text,g.purpose,g.revoked_at IS NOT NULL OR g.expires_at<=now() FROM teaching.capture_connections n JOIN teaching.capture_grants g ON g.id=n.grant_id JOIN teaching.live_captures c ON c.id=n.capture_id`)
	if e != nil {
		return e
	}
	type item struct {
		id, capture, user, purpose string
		expired                    bool
	}
	var items []item
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.id, &v.capture, &v.user, &v.purpose, &v.expired); e != nil {
			break
		}
		items = append(items, v)
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		r, e := load(ctx, s.pool, v.capture, false)
		if e != nil {
			return e
		}
		p, pe := s.identity.PrincipalByID(ctx, v.user)
		invalid := v.expired || pe != nil || s.valid(ctx, s.pool, r) != nil || r.StopAt != nil
		if !invalid {
			if v.purpose == "read" {
				invalid = s.media.AuthorizeCaptureRead(ctx, p, r.SessionID) != nil
			} else {
				invalid = s.media.AuthorizeUpload(ctx, p, r.SessionID) != nil
			}
		}
		if invalid {
			if e = s.control.Kick(ctx, v.id); e != nil {
				return e
			}
			if _, e = s.pool.Exec(ctx, `DELETE FROM teaching.capture_connections WHERE connection_id=$1::uuid`, v.id); e != nil {
				return e
			}
		}
	}
	return nil
}
