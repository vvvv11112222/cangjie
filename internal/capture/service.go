package capture

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
)

func New(pool *pgxpool.Pool, m *media.Service, a *analysis.Service, i *identity.Service, c Config) (*Service, error) {
	if strings.TrimSpace(c.RecordRoot) == "" {
		return nil, fmt.Errorf("CAPTURE_RECORD_ROOT is required")
	}
	for _, v := range []string{c.PublicURL, c.MediaURL} {
		u, e := url.Parse(v)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("capture public and media URLs must use HTTPS")
		}
	}
	if len(c.EventToken) < 32 || len(c.ControlToken) < 32 || c.EventToken == c.ControlToken {
		return nil, fmt.Errorf("capture requires distinct event and media control secrets of at least 32 characters")
	}
	if c.PairingTTL <= 0 {
		c.PairingTTL = 5 * time.Minute
	}
	if c.ViewerTTL <= 0 {
		c.ViewerTTL = 5 * time.Minute
	}
	if c.MaxDuration <= 0 {
		c.MaxDuration = 2 * time.Hour
	}
	if c.WaitingTimeout <= 0 {
		c.WaitingTimeout = 10 * time.Minute
	}
	if c.DisconnectTimeout <= 0 {
		c.DisconnectTimeout = 10 * time.Second
	}
	if c.FinalizeTimeout <= 0 {
		c.FinalizeTimeout = 30 * time.Minute
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 4 << 30
	}
	root, e := filepath.Abs(c.RecordRoot)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	c.RecordRoot = root
	client, e := NewClient(c.ControlURL, c.ControlToken)
	if e != nil {
		return nil, e
	}
	return &Service{pool: pool, media: m, analysis: a, identity: i, cfg: c, control: client}, nil
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const columns = `id::text,session_id::text,state,connection_state,recording_state,audio_track,video_track,created_at,expires_at,
 started_at,ended_at,duration_ms,media_asset_id::text,media_prepare_run_id::text,end_reason,error_code,
 source_record_id::text,created_by::text,stream_path,storage_generation,rights_version,stop_requested_at,disconnected_at`

func load(ctx context.Context, q querier, id string, lock bool) (record, error) {
	var r record
	query := `SELECT ` + columns + ` FROM teaching.live_captures WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	e := q.QueryRow(ctx, query, id).Scan(&r.ID, &r.SessionID, &r.State, &r.ConnectionState, &r.RecordingState, &r.AudioTrack, &r.VideoTrack, &r.CreatedAt, &r.ExpiresAt,
		&r.StartedAt, &r.EndedAt, &r.DurationMS, &r.MediaID, &r.RunID, &r.EndReason, &r.ErrorCode, &r.SourceID, &r.UserID, &r.Path, &r.Generation, &r.Rights, &r.StopAt, &r.DisconnectAt)
	r.AllowedActions = []string{}
	if errors.Is(e, pgx.ErrNoRows) {
		return r, missing()
	}
	return r, e
}
func (s *Service) lock(ctx context.Context, id string) (pgx.Tx, record, error) {
	r, e := load(ctx, s.pool, id, false)
	if e != nil {
		return nil, r, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return nil, r, e
	}
	var sid string
	if e = tx.QueryRow(ctx, `SELECT id::text FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, r.SessionID).Scan(&sid); e == nil {
		r, e = load(ctx, tx, id, true)
	}
	if e != nil {
		_ = tx.Rollback(ctx)
		return nil, r, e
	}
	return tx, r, nil
}
func (s *Service) valid(ctx context.Context, q querier, r record) error {
	var ok bool
	e := q.QueryRow(ctx, `SELECT ls.status NOT IN('deleting','deleted') AND ls.content_expires_at>now()
 AND ls.storage_generation=$3 AND src.rights_status='verified' AND src.allowed_uses ? 'playback' AND src.rights_version=$4
 FROM teaching.lesson_sessions ls JOIN teaching.source_records src ON src.session_id=ls.id AND src.id=$2 WHERE ls.id=$1`, r.SessionID, r.SourceID, r.Generation, r.Rights).Scan(&ok)
	if e != nil {
		return e
	}
	expires := r.ExpiresAt
	if r.State == "finalizing" {
		expires = expires.Add(s.cfg.FinalizeTimeout)
	}
	if !ok || !expires.After(time.Now()) {
		return denied()
	}
	return nil
}
func (s *Service) Create(ctx context.Context, p identity.Principal, session, key string, in Create) (Capture, bool, error) {
	if in.Profile != "phone-demo-v1" || in.SourceID == "" || len(key) < 1 || len(key) > 128 || strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return Capture{}, false, invalid("invalid profile, source or Idempotency-Key")
	}
	if e := s.media.AuthorizeUpload(ctx, p, session); e != nil {
		return Capture{}, false, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Capture{}, false, e
	}
	defer tx.Rollback(ctx)
	var gen int64
	var expiry time.Time
	if e = tx.QueryRow(ctx, `SELECT storage_generation,LEAST(content_expires_at,now()+$2::interval) FROM teaching.lesson_sessions WHERE id=$1 AND status NOT IN('deleting','deleted') AND content_expires_at>now() FOR UPDATE`, session, s.cfg.MaxDuration.String()).Scan(&gen, &expiry); e != nil {
		return Capture{}, false, bad("INVALID_STATE", "lesson does not accept capture")
	}
	var sourceSession, rights string
	var ver int
	var allowed bool
	e = tx.QueryRow(ctx, `SELECT session_id::text,rights_status,rights_version,allowed_uses ? 'playback' FROM teaching.source_records WHERE id=$1 FOR SHARE`, in.SourceID).Scan(&sourceSession, &rights, &ver, &allowed)
	if e != nil || sourceSession != session || rights != "verified" || !allowed {
		return Capture{}, false, denied()
	}
	var prior, d string
	e = tx.QueryRow(ctx, `SELECT id::text,request_sha256 FROM teaching.live_captures WHERE session_id=$1 AND created_by=$2 AND idempotency_key=$3`, session, p.UserID, key).Scan(&prior, &d)
	if e == nil {
		if d != digest(in) {
			return Capture{}, false, bad("IDEMPOTENCY_CONFLICT", "key has different parameters")
		}
		r, e := load(ctx, tx, prior, false)
		return r.Capture, false, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return Capture{}, false, e
	}
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO teaching.live_captures(session_id,source_record_id,created_by,profile,idempotency_key,request_sha256,stream_path,storage_generation,rights_version,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,'pending-'||gen_random_uuid()::text,$7,$8,$9) RETURNING id::text`, session, in.SourceID, p.UserID, in.Profile, key, digest(in), gen, ver, expiry).Scan(&id)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return Capture{}, false, bad("CAPTURE_ACTIVE", "lesson already has an active capture")
		}
		return Capture{}, false, e
	}
	if _, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET stream_path=$2 WHERE id=$1`, id, fmt.Sprintf("captures/%s/%d", id, gen)); e != nil {
		return Capture{}, false, e
	}
	r, e := load(ctx, tx, id, false)
	if e != nil {
		return Capture{}, false, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Capture{}, false, e
	}
	r.AllowedActions = []string{"pair", "read", "stop"}
	return r.Capture, true, nil
}
func (s *Service) Get(ctx context.Context, p identity.Principal, id string) (Capture, error) {
	r, e := load(ctx, s.pool, id, false)
	if e != nil {
		return Capture{}, e
	}
	if e = s.media.AuthorizeCaptureRead(ctx, p, r.SessionID); e != nil {
		return Capture{}, e
	}
	r.AllowedActions = []string{"read"}
	if s.valid(ctx, s.pool, r) == nil && s.media.AuthorizeUpload(ctx, p, r.SessionID) == nil && (r.State == "waiting" || r.State == "recording") {
		r.AllowedActions = append(r.AllowedActions, "stop")
		if r.State == "waiting" {
			r.AllowedActions = append(r.AllowedActions, "pair")
		}
	}
	if r.State == "recording" && s.valid(ctx, s.pool, r) == nil {
		r.AllowedActions = append(r.AllowedActions, "watch")
	}
	return r.Capture, nil
}

// Active returns the single non-terminal capture for a lesson. The partial
// unique index in migration 009 enforces that there can be at most one.
func (s *Service) Active(ctx context.Context, p identity.Principal, session string) (Capture, error) {
	if e := s.media.AuthorizeCaptureRead(ctx, p, session); e != nil {
		return Capture{}, e
	}
	var id string
	if e := s.pool.QueryRow(ctx, `SELECT id::text FROM teaching.live_captures WHERE session_id=$1 AND state IN('waiting','recording','finalizing')`, session).Scan(&id); errors.Is(e, pgx.ErrNoRows) {
		return Capture{}, missing()
	} else if e != nil {
		return Capture{}, e
	}
	return s.Get(ctx, p, id)
}
func (s *Service) Pair(ctx context.Context, p identity.Principal, id string) (Pairing, error) {
	tx, r, e := s.lock(ctx, id)
	if e != nil {
		return Pairing{}, e
	}
	defer tx.Rollback(ctx)
	if e = s.media.AuthorizeUpload(ctx, p, r.SessionID); e != nil {
		return Pairing{}, e
	}
	if e = s.valid(ctx, tx, r); e != nil {
		return Pairing{}, e
	}
	if r.State != "waiting" {
		return Pairing{}, bad("INVALID_STATE", "capture is not waiting")
	}
	var active bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.capture_grants WHERE capture_id=$1 AND purpose='publish' AND revoked_at IS NULL)`, id).Scan(&active); e != nil {
		return Pairing{}, e
	}
	if active {
		return Pairing{}, bad("INVALID_STATE", "publisher is already paired; stop and create a new capture")
	}
	token, e := identity.RandomToken()
	if e != nil {
		return Pairing{}, e
	}
	expiry := time.Now().Add(s.cfg.PairingTTL)
	if r.ExpiresAt.Before(expiry) {
		expiry = r.ExpiresAt
	}
	if _, e = tx.Exec(ctx, `UPDATE teaching.capture_pairings SET revoked_at=now() WHERE capture_id=$1 AND consumed_at IS NULL`, id); e != nil {
		return Pairing{}, e
	}
	v := Pairing{ExpiresAt: expiry, URL: strings.TrimRight(s.cfg.PublicURL, "/") + "/capture-demo.html#pairing_token=" + token}
	if e = tx.QueryRow(ctx, `INSERT INTO teaching.capture_pairings(capture_id,token_sha256,expires_at) VALUES($1,$2,$3) RETURNING id::text`, id, hash(token), expiry).Scan(&v.ID); e != nil {
		return Pairing{}, e
	}
	return v, tx.Commit(ctx)
}
func (s *Service) grant(ctx context.Context, tx pgx.Tx, r record, user, purpose string, expiry time.Time) (string, string, error) {
	token, e := identity.RandomToken()
	if e != nil {
		return "", "", e
	}
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO teaching.capture_grants(capture_id,user_id,purpose,token_sha256,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, r.ID, user, purpose, hash(token), expiry).Scan(&id)
	return id, token, e
}
func (s *Service) Exchange(ctx context.Context, in Exchange) (Publisher, error) {
	if len(in.Token) != 64 {
		return Publisher{}, denied()
	}
	var id string
	if e := s.pool.QueryRow(ctx, `SELECT capture_id::text FROM teaching.capture_pairings WHERE token_sha256=$1`, hash(in.Token)).Scan(&id); e != nil {
		return Publisher{}, denied()
	}
	tx, r, e := s.lock(ctx, id)
	if e != nil {
		return Publisher{}, e
	}
	defer tx.Rollback(ctx)
	if r.State != "waiting" {
		return Publisher{}, denied()
	}
	if e = s.valid(ctx, tx, r); e != nil {
		return Publisher{}, e
	}
	p, e := s.identity.PrincipalByID(ctx, r.UserID)
	if e != nil {
		return Publisher{}, e
	}
	if e = s.media.AuthorizeUpload(ctx, p, r.SessionID); e != nil {
		return Publisher{}, e
	}
	tag, e := tx.Exec(ctx, `UPDATE teaching.capture_pairings SET consumed_at=now() WHERE capture_id=$1 AND token_sha256=$2 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>now()`, id, hash(in.Token))
	if e != nil {
		return Publisher{}, e
	}
	if tag.RowsAffected() != 1 {
		return Publisher{}, denied()
	}
	_, pub, e := s.grant(ctx, tx, r, p.UserID, "publish", r.ExpiresAt)
	if e != nil {
		return Publisher{}, e
	}
	_, control, e := s.grant(ctx, tx, r, p.UserID, "control", r.ExpiresAt.Add(s.cfg.FinalizeTimeout))
	if e != nil {
		return Publisher{}, e
	}
	v := Publisher{CaptureID: id, SessionID: r.SessionID, WHIPURL: strings.TrimRight(s.cfg.MediaURL, "/") + "/" + r.Path + "/whip", PublishToken: pub, ControlToken: control, ExpiresAt: r.ExpiresAt}
	return v, tx.Commit(ctx)
}
func (s *Service) ControlPrincipal(ctx context.Context, id, token string) (identity.Principal, error) {
	var user string
	if len(token) != 64 {
		return identity.Principal{}, denied()
	}
	e := s.pool.QueryRow(ctx, `SELECT user_id::text FROM teaching.capture_grants WHERE capture_id=$1 AND token_sha256=$2 AND purpose='control' AND revoked_at IS NULL AND expires_at>now()`, id, hash(token)).Scan(&user)
	if e != nil {
		return identity.Principal{}, denied()
	}
	return s.identity.PrincipalByID(ctx, user)
}
func (s *Service) View(ctx context.Context, p identity.Principal, id string) (Viewer, error) {
	tx, r, e := s.lock(ctx, id)
	if e != nil {
		return Viewer{}, e
	}
	defer tx.Rollback(ctx)
	if e = s.media.AuthorizeCaptureRead(ctx, p, r.SessionID); e != nil {
		return Viewer{}, e
	}
	if e = s.valid(ctx, tx, r); e != nil {
		return Viewer{}, e
	}
	if r.State != "recording" || r.ConnectionState != "connected" {
		return Viewer{}, bad("INVALID_STATE", "capture is not online")
	}
	expiry := time.Now().Add(s.cfg.ViewerTTL)
	if r.ExpiresAt.Before(expiry) {
		expiry = r.ExpiresAt
	}
	gid, token, e := s.grant(ctx, tx, r, p.UserID, "read", expiry)
	if e != nil {
		return Viewer{}, e
	}
	return Viewer{ID: gid, URL: strings.TrimRight(s.cfg.MediaURL, "/") + "/" + r.Path + "/whep", Token: token, ExpiresAt: expiry}, tx.Commit(ctx)
}
func (s *Service) Stop(ctx context.Context, p identity.Principal, id string) (Capture, error) {
	tx, r, e := s.lock(ctx, id)
	if e != nil {
		return Capture{}, e
	}
	defer tx.Rollback(ctx)
	if e = s.media.AuthorizeUpload(ctx, p, r.SessionID); e != nil {
		return Capture{}, e
	}
	if r.State == "waiting" || r.State == "recording" {
		_, e = tx.Exec(ctx, `UPDATE teaching.live_captures SET state='finalizing',stop_requested_at=now(),recording_state='stopping',end_reason='user_stopped' WHERE id=$1`, id)
		if e != nil {
			return Capture{}, e
		}
		if _, e = tx.Exec(ctx, `UPDATE teaching.capture_grants SET revoked_at=now() WHERE capture_id=$1 AND purpose IN('read','publish')`, id); e != nil {
			return Capture{}, e
		}
	}
	r, e = load(ctx, tx, id, false)
	if e != nil {
		return Capture{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Capture{}, e
	}
	return r.Capture, nil
}
func (s *Service) Authenticate(ctx context.Context, in AuthRequest) error {
	if in.Action == "api" {
		if identity.SecureEqual(in.Password, s.cfg.ControlToken) || identity.SecureEqual(in.Token, s.cfg.ControlToken) {
			return nil
		}
		return denied()
	}
	if in.Protocol != "webrtc" || (in.Action != "publish" && in.Action != "read") || len(in.Token) != 64 {
		return denied()
	}
	var id, user, grantID string
	if len(in.ID) != 36 {
		return denied()
	}
	if b, e := hex.DecodeString(strings.ReplaceAll(in.ID, "-", "")); e != nil || len(b) != 16 {
		return denied()
	}
	if e := s.pool.QueryRow(ctx, `SELECT c.id::text,g.user_id::text,g.id::text FROM teaching.capture_grants g JOIN teaching.live_captures c ON c.id=g.capture_id WHERE g.token_sha256=$1 AND g.purpose=$2 AND c.stream_path=$3 AND g.revoked_at IS NULL AND g.expires_at>now()`, hash(in.Token), in.Action, in.Path).Scan(&id, &user, &grantID); e != nil {
		return denied()
	}
	r, e := load(ctx, s.pool, id, false)
	if e != nil {
		return e
	}
	if e = s.valid(ctx, s.pool, r); e != nil {
		return e
	}
	p, e := s.identity.PrincipalByID(ctx, user)
	if e != nil {
		return e
	}
	if in.Action == "publish" {
		if r.State != "waiting" && r.State != "recording" {
			return denied()
		}
		if r.DisconnectAt != nil || r.StopAt != nil {
			return denied()
		}
		e = s.media.AuthorizeUpload(ctx, p, r.SessionID)
	} else {
		if r.State != "recording" || r.StopAt != nil {
			return denied()
		}
		e = s.media.AuthorizeCaptureRead(ctx, p, r.SessionID)
	}
	if e != nil {
		return e
	}
	_, e = s.pool.Exec(ctx, `INSERT INTO teaching.capture_connections(connection_id,grant_id,capture_id) VALUES($1::uuid,$2,$3) ON CONFLICT(connection_id) DO NOTHING`, in.ID, grantID, id)
	return e
}
