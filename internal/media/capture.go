package media

import (
	"context"
	"fmt"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"io"
	"time"
)

func (s *Service) AuthorizeCaptureRead(ctx context.Context, p identity.Principal, session string) error {
	scope, e := s.loadSession(ctx, s.pool, session, false)
	if e != nil {
		return e
	}
	if !canViewContent(p, scope) {
		return notFound("lesson")
	}
	return nil
}

// ImportRecording stages bytes with the existing storage adapter and atomically
// binds the source asset to the capture. A lost commit acknowledgement is retryable.
func (s *Service) ImportRecording(ctx context.Context, p identity.Principal, id, lease string, input io.Reader) (string, error) {
	staged, e := s.store.Stage(ctx, input, s.maxBytes)
	if e != nil {
		return "", e
	}
	defer s.store.Discard(staged)
	if !validContainer(".mp4", staged.Header) {
		return "", invalid("recording is not MP4")
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var session string
	if e = tx.QueryRow(ctx, `SELECT session_id::text FROM teaching.live_captures WHERE id=$1`, id).Scan(&session); e != nil {
		return "", e
	}
	scope, e := s.loadSession(ctx, tx, session, true)
	if e != nil {
		return "", e
	}
	var source, state, rights string
	var gen int64
	var ver, currentVer int
	var existing *string
	var allowed bool
	e = tx.QueryRow(ctx, `SELECT source_record_id::text,storage_generation,rights_version,state,media_asset_id::text FROM teaching.live_captures WHERE id=$1 AND finalize_token=$2::uuid AND finalize_until>clock_timestamp() FOR UPDATE`, id, lease).Scan(&source, &gen, &ver, &state, &existing)
	if e != nil {
		return "", e
	}
	if existing != nil {
		return *existing, nil
	}
	if scope.Generation != gen || scope.Status == "deleting" || scope.Status == "deleted" || !scope.ContentExpiresAt.After(time.Now()) || !canUpload(p, scope) || state != "finalizing" {
		return "", invalid("capture is no longer writable")
	}
	e = tx.QueryRow(ctx, `SELECT rights_status,rights_version,allowed_uses ? 'playback' FROM teaching.source_records WHERE id=$1 AND session_id=$2 FOR SHARE`, source, session).Scan(&rights, &currentVer, &allowed)
	if e != nil {
		return "", e
	}
	if rights != "verified" || ver != currentVer || !allowed {
		return "", forbidden("source rights changed")
	}
	key, e := randomObjectKey(session, ".mp4")
	if e != nil {
		return "", e
	}
	if e = s.store.Commit(staged, key); e != nil {
		return "", e
	}
	remove := true
	defer func() {
		if remove {
			_ = s.store.Remove(key)
		}
	}()
	expiry := time.Now().Add(s.retention)
	if scope.ContentExpiresAt.Before(expiry) {
		expiry = scope.ContentExpiresAt
	}
	var mediaID string
	e = tx.QueryRow(ctx, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,status,expires_at) VALUES($1,$2,'source','filesystem',$3,$4,'video/mp4',$5,'pending',$6) RETURNING id::text`, session, source, key, staged.SHA256, staged.Size, expiry).Scan(&mediaID)
	if e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET media_asset_id=$2 WHERE id=$1`, id, mediaID); e != nil {
		return "", e
	}
	remove = false // uncertain COMMIT must not remove a possibly committed object
	if e = tx.Commit(ctx); e != nil {
		return "", fmt.Errorf("commit recording import: %w", e)
	}
	return mediaID, nil
}
