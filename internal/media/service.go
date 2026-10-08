package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/optional"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

type Service struct {
	pool      *pgxpool.Pool
	store     storage.Backend
	maxBytes  int64
	retention time.Duration
	now       func() time.Time
}

func NewService(pool *pgxpool.Pool, store storage.Backend, maxBytes int64, retention time.Duration) *Service {
	return &Service{pool: pool, store: store, maxBytes: maxBytes, retention: retention, now: time.Now}
}

type CreateSource struct {
	SessionID                   string                 `json:"session_id"`
	SourceType                  string                 `json:"source_type"`
	Title                       string                 `json:"title"`
	Attribution                 string                 `json:"attribution"`
	SourceURL                   optional.Value[string] `json:"source_url"`
	AuthorizationNote           *string                `json:"authorization_note"`
	RequestedUses               []string               `json:"requested_uses"`
	ExternalProcessingRequested *bool                  `json:"external_processing_requested"`
}

type VerifySource struct {
	RightsStatus              string   `json:"rights_status"`
	AllowedUses               []string `json:"allowed_uses"`
	ExternalProcessingAllowed *bool    `json:"external_processing_allowed"`
	Reason                    string   `json:"reason"`
}

type Source struct {
	ID                          string     `json:"id"`
	SessionID                   *string    `json:"session_id"`
	SourceType                  string     `json:"source_type"`
	Title                       string     `json:"title"`
	Attribution                 string     `json:"attribution"`
	SourceURL                   *string    `json:"source_url"`
	AuthorizationNote           string     `json:"authorization_note"`
	RequestedUses               []string   `json:"requested_uses"`
	ExternalProcessingRequested bool       `json:"external_processing_requested"`
	RightsStatus                string     `json:"rights_status"`
	AllowedUses                 []string   `json:"allowed_uses"`
	ExternalProcessingAllowed   bool       `json:"external_processing_allowed"`
	RightsVersion               int        `json:"rights_version"`
	VerifiedBy                  *string    `json:"verified_by"`
	VerifiedAt                  *time.Time `json:"verified_at"`
	AllowedActions              []string   `json:"allowed_actions"`
}

type Media struct {
	ID              string     `json:"id"`
	SessionID       string     `json:"session_id"`
	SourceRecordID  *string    `json:"source_record_id"`
	ParentAssetID   *string    `json:"parent_asset_id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	SHA256          string     `json:"sha256"`
	MIMEType        string     `json:"mime_type"`
	ByteSize        int64      `json:"byte_size"`
	DurationMS      *int64     `json:"duration_ms"`
	ExpiresAt       *time.Time `json:"expires_at"`
	PlaybackAssetID *string    `json:"playback_asset_id"`
	PlaybackURL     *string    `json:"playback_url"`
	AllowedActions  []string   `json:"allowed_actions"`
}

type sessionScope struct {
	CollegeID        string
	TeacherID        string
	Status           string
	ContentExpiresAt time.Time
	Generation       int64
}

func (s *Service) CreateSource(ctx context.Context, p identity.Principal, in CreateSource) (Source, error) {
	if !in.SourceURL.Set || in.AuthorizationNote == nil || in.RequestedUses == nil || in.ExternalProcessingRequested == nil {
		return Source{}, invalid("source_url, authorization_note, requested_uses, and external_processing_requested are required")
	}
	in.Title, in.Attribution = strings.TrimSpace(in.Title), strings.TrimSpace(in.Attribution)
	if in.Title == "" || in.Attribution == "" {
		return Source{}, invalid("title and attribution are required")
	}
	if !oneOf(in.SourceType, "self_recorded", "open_course", "school_authorized") {
		return Source{}, invalid("source_type is invalid")
	}
	uses, err := validateUses(in.RequestedUses)
	if err != nil {
		return Source{}, err
	}
	var sourceURL *string
	if in.SourceURL.Value != nil {
		value := strings.TrimSpace(*in.SourceURL.Value)
		parsed, parseErr := url.ParseRequestURI(value)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" {
			return Source{}, invalid("source_url must be an absolute URI or null")
		}
		sourceURL = &value
	}
	scope, err := s.loadSession(ctx, s.pool, in.SessionID, false)
	if err != nil {
		return Source{}, err
	}
	if !canUpload(p, scope) {
		return Source{}, forbidden("source registration is outside your role and scope")
	}
	if scope.Status == "deleting" || scope.Status == "deleted" || !scope.ContentExpiresAt.After(s.now()) {
		return Source{}, state("the lesson no longer accepts sources")
	}
	requested, _ := json.Marshal(uses)
	row := s.pool.QueryRow(ctx, `INSERT INTO teaching.source_records(session_id,source_type,title,source_url,attribution,authorization_note,registered_by,requested_uses,external_processing_requested)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+sourceColumns,
		in.SessionID, in.SourceType, in.Title, sourceURL, in.Attribution, *in.AuthorizationNote, p.UserID, requested, *in.ExternalProcessingRequested)
	result, err := scanSource(row)
	if err != nil {
		return Source{}, storageError(err)
	}
	result.AllowedActions = sourceActions(p, scope, result)
	return result, nil
}

func (s *Service) VerifySource(ctx context.Context, p identity.Principal, id string, in VerifySource) (Source, error) {
	if in.ExternalProcessingAllowed == nil || strings.TrimSpace(in.Reason) == "" {
		return Source{}, invalid("external_processing_allowed and reason are required")
	}
	if !oneOf(in.RightsStatus, "verified", "rejected") {
		return Source{}, invalid("rights_status is invalid")
	}
	uses, err := validateUses(in.AllowedUses)
	if err != nil {
		return Source{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Source{}, fmt.Errorf("begin source verification: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var sessionID *string
	if err := tx.QueryRow(ctx, `SELECT session_id::text FROM teaching.source_records WHERE id=$1`, id).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return Source{}, notFound("source")
	} else if err != nil {
		return Source{}, storageError(err)
	}
	if sessionID == nil {
		return Source{}, state("unassigned historical sources must be assigned before verification")
	}
	scope, err := s.loadSession(ctx, tx, *sessionID, true)
	if err != nil {
		return Source{}, err
	}
	if !canVerify(p, scope) {
		return Source{}, forbidden("source verification is outside your role and scope")
	}
	current, err := scanSource(tx.QueryRow(ctx, `SELECT `+sourceColumns+` FROM teaching.source_records WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return Source{}, storageError(err)
	}
	if in.RightsStatus == "rejected" && (len(uses) != 0 || *in.ExternalProcessingAllowed) {
		return Source{}, invalid("rejected sources cannot retain allowed uses or external processing")
	}
	if !subset(uses, current.RequestedUses) || (*in.ExternalProcessingAllowed && !current.ExternalProcessingRequested) {
		return Source{}, invalid("verification cannot grant uses that were not requested")
	}
	allowed, _ := json.Marshal(uses)
	result, err := scanSource(tx.QueryRow(ctx, `UPDATE teaching.source_records SET rights_status=$2,allowed_uses=$3,external_processing_allowed=$4,
		verified_by=$5,verified_at=$6,rights_version=rights_version+1 WHERE id=$1 RETURNING `+sourceColumns,
		id, in.RightsStatus, allowed, *in.ExternalProcessingAllowed, p.UserID, s.now()))
	if err != nil {
		return Source{}, storageError(err)
	}
	metadata, _ := json.Marshal(map[string]any{"reason": strings.TrimSpace(in.Reason), "rights_status": in.RightsStatus, "allowed_uses": uses})
	if _, err := tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata)
		VALUES($1,'source.verify','source',$2,'success','source-verification',$3)`, p.UserID, id, metadata); err != nil {
		return Source{}, storageError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Source{}, storageError(err)
	}
	result.AllowedActions = sourceActions(p, scope, result)
	return result, nil
}

func (s *Service) AssignSource(ctx context.Context, p identity.Principal, id, sessionID string) (Source, error) {
	if !p.Has("sys_admin") {
		return Source{}, forbidden("only a system administrator can assign historical sources")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Source{}, fmt.Errorf("begin source assignment: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	scope, err := s.loadSession(ctx, tx, sessionID, true)
	if err != nil {
		return Source{}, err
	}
	result, err := scanSource(tx.QueryRow(ctx, `UPDATE teaching.source_records SET session_id=$2 WHERE id=$1 AND session_id IS NULL
		AND NOT EXISTS(SELECT 1 FROM teaching.media_assets WHERE source_record_id=$1 AND session_id<>$2) RETURNING `+sourceColumns, id, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, state("source is already assigned or has media in another lesson")
	}
	if err != nil {
		return Source{}, storageError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Source{}, storageError(err)
	}
	result.AllowedActions = sourceActions(p, scope, result)
	return result, nil
}

func (s *Service) GetSource(ctx context.Context, p identity.Principal, id string) (Source, error) {
	result, err := scanSource(s.pool.QueryRow(ctx, `SELECT `+sourceColumns+` FROM teaching.source_records WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) || result.SessionID == nil {
		if p.Has("sys_admin") && err == nil {
			result.AllowedActions = []string{"assign_session"}
			return result, nil
		}
		return Source{}, notFound("source")
	}
	if err != nil {
		return Source{}, storageError(err)
	}
	scope, err := s.loadSession(ctx, s.pool, *result.SessionID, false)
	if err != nil || !canViewSource(p, scope) {
		return Source{}, notFound("source")
	}
	result.AllowedActions = sourceActions(p, scope, result)
	return result, nil
}

func (s *Service) ListSources(ctx context.Context, p identity.Principal, sessionID, after string, limit int) ([]Source, string, error) {
	scope, err := s.loadSession(ctx, s.pool, sessionID, false)
	if err != nil || !canViewSource(p, scope) {
		return nil, "", notFound("lesson")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+sourceColumns+` FROM teaching.source_records WHERE session_id=$1 AND ($2='' OR id>$2::uuid) ORDER BY id LIMIT $3`, sessionID, after, limit+1)
	if err != nil {
		return nil, "", storageError(err)
	}
	defer rows.Close()
	items := []Source{}
	for rows.Next() {
		item, scanErr := scanSource(rows)
		if scanErr != nil {
			return nil, "", storageError(scanErr)
		}
		item.AllowedActions = sourceActions(p, scope, item)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", storageError(err)
	}
	return paginateSources(items, limit)
}

func (s *Service) Upload(ctx context.Context, p identity.Principal, sessionID, sourceID, filename, declaredMIME string, reader io.Reader) (Media, error) {
	scope, err := s.loadSession(ctx, s.pool, sessionID, false)
	if err != nil {
		return Media{}, err
	}
	if !canUpload(p, scope) {
		return Media{}, forbidden("media upload is outside your role and scope")
	}
	if scope.Status == "deleting" || scope.Status == "deleted" || !scope.ContentExpiresAt.After(s.now()) {
		return Media{}, state("the lesson no longer accepts media")
	}
	var sourceSession string
	if err := s.pool.QueryRow(ctx, `SELECT session_id::text FROM teaching.source_records WHERE id=$1`, sourceID).Scan(&sourceSession); errors.Is(err, pgx.ErrNoRows) || sourceSession != sessionID {
		return Media{}, invalid("source_record_id must belong to the lesson")
	} else if err != nil {
		return Media{}, storageError(err)
	}
	mimeType, extension, err := mediaType(filename, declaredMIME)
	if err != nil {
		return Media{}, err
	}
	staged, err := s.store.Stage(ctx, reader, s.maxBytes)
	if errors.Is(err, storage.ErrTooLarge) {
		return Media{}, apperror.New(http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "media exceeds MAX_UPLOAD_BYTES")
	}
	if errors.Is(err, storage.ErrEmpty) {
		return Media{}, apperror.New(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA", "uploaded media is empty")
	}
	if err != nil {
		return Media{}, fmt.Errorf("stage media upload: %w", err)
	}
	defer s.store.Discard(staged) //nolint:errcheck
	if !validContainer(extension, staged.Header) {
		return Media{}, apperror.New(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA", "file contents do not match the declared MP4, MOV, or MKV container")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Media{}, fmt.Errorf("begin media upload: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	locked, err := s.loadSession(ctx, tx, sessionID, true)
	if err != nil {
		return Media{}, err
	}
	if locked.Generation != scope.Generation || locked.Status == "deleting" || locked.Status == "deleted" || !locked.ContentExpiresAt.After(s.now()) || !canUpload(p, locked) {
		return Media{}, state("lesson state changed while media was uploading")
	}
	if err := tx.QueryRow(ctx, `SELECT session_id::text FROM teaching.source_records WHERE id=$1 FOR UPDATE`, sourceID).Scan(&sourceSession); err != nil || sourceSession != sessionID {
		if errors.Is(err, pgx.ErrNoRows) || sourceSession != sessionID {
			return Media{}, invalid("source_record_id must belong to the lesson")
		}
		return Media{}, storageError(err)
	}
	objectKey, err := randomObjectKey(sessionID, extension)
	if err != nil {
		return Media{}, err
	}
	if err := s.store.Commit(staged, objectKey); err != nil {
		return Media{}, err
	}
	committed := true
	defer func() {
		if committed {
			_ = s.store.Remove(objectKey)
		}
	}()
	expires := s.now().Add(s.retention)
	if locked.ContentExpiresAt.Before(expires) {
		expires = locked.ContentExpiresAt
	}
	result, err := scanMedia(tx.QueryRow(ctx, `INSERT INTO teaching.media_assets AS m(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,status,expires_at)
		VALUES($1,$2,'source','filesystem',$3,$4,$5,$6,'pending',$7) RETURNING `+mediaColumns,
		sessionID, sourceID, objectKey, staged.SHA256, mimeType, staged.Size, expires))
	if err != nil {
		return Media{}, storageError(err)
	}
	// Once COMMIT starts its outcome can be uncertain to this process. Preserve the
	// object on commit errors so reconciliation can remove an orphan instead of
	// risking a committed database row that points at a deleted file.
	committed = false
	if err := tx.Commit(ctx); err != nil {
		return Media{}, storageError(err)
	}
	result.AllowedActions = mediaActions(p, locked, result, "pending", nil, false)
	return result, nil
}

func (s *Service) AuthorizeUpload(ctx context.Context, p identity.Principal, sessionID string) error {
	scope, err := s.loadSession(ctx, s.pool, sessionID, false)
	if err != nil {
		return err
	}
	if !canUpload(p, scope) {
		return forbidden("media upload is outside your role and scope")
	}
	if scope.Status == "deleting" || scope.Status == "deleted" || !scope.ContentExpiresAt.After(s.now()) {
		return state("the lesson no longer accepts media")
	}
	return nil
}

func (s *Service) GetMedia(ctx context.Context, p identity.Principal, id string) (Media, error) {
	result, rights, allowed, playbackAvailable, err := s.loadMedia(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, notFound("media")
	}
	if err != nil {
		return Media{}, storageError(err)
	}
	scope, err := s.loadSession(ctx, s.pool, result.SessionID, false)
	if err != nil || !canViewContent(p, scope) {
		return Media{}, notFound("media")
	}
	result.AllowedActions = mediaActions(p, scope, result, rights, allowed, playbackAvailable)
	setPlaybackURL(&result, scope, rights, allowed, playbackAvailable, s.now())
	return result, nil
}

func (s *Service) ListMedia(ctx context.Context, p identity.Principal, sessionID, after string, limit int) ([]Media, string, error) {
	scope, err := s.loadSession(ctx, s.pool, sessionID, false)
	if err != nil || !canViewContent(p, scope) {
		return nil, "", notFound("lesson")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+mediaColumns+`,COALESCE(src.rights_status,parent_src.rights_status,'pending'),COALESCE(src.allowed_uses,parent_src.allowed_uses,'[]'::jsonb),
		EXISTS(SELECT 1 FROM teaching.media_assets playback WHERE playback.id=CASE WHEN m.kind='source' THEN m.playback_asset_id ELSE m.id END
			AND playback.session_id=m.session_id AND playback.status='ready' AND playback.expires_at>now())
		FROM teaching.media_assets m LEFT JOIN teaching.source_records src ON src.id=m.source_record_id AND src.session_id=m.session_id
		LEFT JOIN teaching.media_assets parent ON parent.id=m.parent_asset_id AND parent.session_id=m.session_id
		LEFT JOIN teaching.source_records parent_src ON parent_src.id=parent.source_record_id AND parent_src.session_id=m.session_id
		WHERE m.session_id=$1 AND m.status<>'deleted' AND ($2='' OR m.id>$2::uuid) ORDER BY m.id LIMIT $3`, sessionID, after, limit+1)
	if err != nil {
		return nil, "", storageError(err)
	}
	defer rows.Close()
	items := []Media{}
	for rows.Next() {
		var item Media
		var rights string
		var allowedRaw []byte
		var playbackAvailable bool
		if err := scanMediaFields(rows, &item, &rights, &allowedRaw, &playbackAvailable); err != nil {
			return nil, "", storageError(err)
		}
		var allowed []string
		_ = json.Unmarshal(allowedRaw, &allowed)
		item.AllowedActions = mediaActions(p, scope, item, rights, allowed, playbackAvailable)
		setPlaybackURL(&item, scope, rights, allowed, playbackAvailable, s.now())
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", storageError(err)
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

type Content struct {
	File     storage.ReadSeekCloser
	Size     int64
	MIMEType string
}

func (s *Service) OpenContent(ctx context.Context, p identity.Principal, id string) (Content, error) {
	var sessionID, kind, status, objectKey, mimeType, rights string
	var byteSize int64
	var mediaExpires, contentExpires time.Time
	var college, teacher string
	var allowedRaw []byte
	err := s.pool.QueryRow(ctx, `SELECT m.session_id::text,m.kind,m.status,m.object_key,m.mime_type,m.byte_size,m.expires_at,
		ls.content_expires_at,teaching.college_of(o.org_unit_id)::text,o.teacher_id::text,
		COALESCE(src.rights_status,parent_src.rights_status,'pending'),COALESCE(src.allowed_uses,parent_src.allowed_uses,'[]'::jsonb)
		FROM teaching.media_assets m JOIN teaching.lesson_sessions ls ON ls.id=m.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id
		LEFT JOIN teaching.source_records src ON src.id=m.source_record_id AND src.session_id=m.session_id
		LEFT JOIN teaching.media_assets parent ON parent.id=m.parent_asset_id AND parent.session_id=m.session_id
		LEFT JOIN teaching.source_records parent_src ON parent_src.id=parent.source_record_id AND parent_src.session_id=m.session_id
		WHERE m.id=$1 AND ls.status NOT IN ('deleting','deleted')`, id).
		Scan(&sessionID, &kind, &status, &objectKey, &mimeType, &byteSize, &mediaExpires, &contentExpires, &college, &teacher, &rights, &allowedRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Content{}, notFound("media")
	}
	if err != nil {
		return Content{}, storageError(err)
	}
	if !canViewContent(p, sessionScope{CollegeID: college, TeacherID: teacher}) {
		return Content{}, notFound("media")
	}
	if rights != "verified" {
		return Content{}, apperror.New(http.StatusForbidden, "SOURCE_NOT_VERIFIED", "source is not verified for playback")
	}
	var allowed []string
	_ = json.Unmarshal(allowedRaw, &allowed)
	if !contains(allowed, "playback") {
		return Content{}, forbidden("source does not allow playback")
	}
	if status != "ready" || !oneOf(kind, "source", "proxy", "audio", "keyframe") {
		return Content{}, state("media is not ready for playback")
	}
	if !mediaExpires.After(s.now()) || !contentExpires.After(s.now()) {
		return Content{}, apperror.New(http.StatusGone, "RESOURCE_EXPIRED", "media has expired")
	}
	file, err := s.store.Open(objectKey)
	if err != nil {
		return Content{}, fmt.Errorf("open media object: %w", err)
	}
	return Content{File: file, Size: byteSize, MIMEType: mimeType}, nil
}

const sourceColumns = `id::text,session_id::text,source_type,title,attribution,source_url,authorization_note,requested_uses,external_processing_requested,
	rights_status,allowed_uses,external_processing_allowed,rights_version,verified_by::text,verified_at`

func scanSource(row pgx.Row) (Source, error) {
	var result Source
	var requestedRaw, allowedRaw []byte
	err := row.Scan(&result.ID, &result.SessionID, &result.SourceType, &result.Title, &result.Attribution, &result.SourceURL, &result.AuthorizationNote,
		&requestedRaw, &result.ExternalProcessingRequested, &result.RightsStatus, &allowedRaw, &result.ExternalProcessingAllowed,
		&result.RightsVersion, &result.VerifiedBy, &result.VerifiedAt)
	if err == nil {
		_ = json.Unmarshal(requestedRaw, &result.RequestedUses)
		_ = json.Unmarshal(allowedRaw, &result.AllowedUses)
		if result.RequestedUses == nil {
			result.RequestedUses = []string{}
		}
		if result.AllowedUses == nil {
			result.AllowedUses = []string{}
		}
	}
	return result, err
}

const mediaColumns = `m.id::text,m.session_id::text,m.source_record_id::text,m.parent_asset_id::text,m.kind,m.status,m.sha256,m.mime_type,m.byte_size,m.duration_ms,m.expires_at,m.playback_asset_id::text`

func scanMedia(row pgx.Row) (Media, error) {
	var result Media
	err := row.Scan(&result.ID, &result.SessionID, &result.SourceRecordID, &result.ParentAssetID, &result.Kind, &result.Status,
		&result.SHA256, &result.MIMEType, &result.ByteSize, &result.DurationMS, &result.ExpiresAt, &result.PlaybackAssetID)
	return result, err
}

func scanMediaFields(row pgx.Row, result *Media, rights *string, allowed *[]byte, playbackAvailable *bool) error {
	return row.Scan(&result.ID, &result.SessionID, &result.SourceRecordID, &result.ParentAssetID, &result.Kind, &result.Status,
		&result.SHA256, &result.MIMEType, &result.ByteSize, &result.DurationMS, &result.ExpiresAt, &result.PlaybackAssetID, rights, allowed, playbackAvailable)
}

func (s *Service) loadMedia(ctx context.Context, id string) (Media, string, []string, bool, error) {
	var result Media
	var rights string
	var allowedRaw []byte
	var playbackAvailable bool
	err := scanMediaFields(s.pool.QueryRow(ctx, `SELECT `+mediaColumns+`,COALESCE(src.rights_status,parent_src.rights_status,'pending'),COALESCE(src.allowed_uses,parent_src.allowed_uses,'[]'::jsonb),
		EXISTS(SELECT 1 FROM teaching.media_assets playback WHERE playback.id=CASE WHEN m.kind='source' THEN m.playback_asset_id ELSE m.id END
			AND playback.session_id=m.session_id AND playback.status='ready' AND playback.expires_at>now())
		FROM teaching.media_assets m LEFT JOIN teaching.source_records src ON src.id=m.source_record_id AND src.session_id=m.session_id
		LEFT JOIN teaching.media_assets parent ON parent.id=m.parent_asset_id AND parent.session_id=m.session_id
		LEFT JOIN teaching.source_records parent_src ON parent_src.id=parent.source_record_id AND parent_src.session_id=m.session_id WHERE m.id=$1`, id), &result, &rights, &allowedRaw, &playbackAvailable)
	var allowed []string
	_ = json.Unmarshal(allowedRaw, &allowed)
	return result, rights, allowed, playbackAvailable, err
}

type sessionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) loadSession(ctx context.Context, q sessionQuerier, id string, lock bool) (sessionScope, error) {
	query := `SELECT teaching.college_of(o.org_unit_id)::text,o.teacher_id::text,ls.status,ls.content_expires_at,ls.storage_generation
		FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE ls.id=$1`
	if lock {
		query += ` FOR UPDATE OF ls`
	}
	var result sessionScope
	err := q.QueryRow(ctx, query, id).Scan(&result.CollegeID, &result.TeacherID, &result.Status, &result.ContentExpiresAt, &result.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionScope{}, notFound("lesson")
	}
	if err != nil {
		return sessionScope{}, storageError(err)
	}
	return result, nil
}

func canUpload(p identity.Principal, scope sessionScope) bool {
	return p.Has("sys_admin") || (p.Has("teacher") && p.UserID == scope.TeacherID)
}

func canVerify(p identity.Principal, scope sessionScope) bool {
	return p.Has("sys_admin") || p.Scoped("academic_admin", scope.CollegeID)
}

func canViewSource(p identity.Principal, scope sessionScope) bool {
	return p.Has("sys_admin") || p.Scoped("academic_admin", scope.CollegeID) || p.Scoped("supervisor", scope.CollegeID) || (p.Has("teacher") && p.UserID == scope.TeacherID)
}

func canViewContent(p identity.Principal, scope sessionScope) bool {
	return p.Has("sys_admin") || p.Scoped("supervisor", scope.CollegeID) || (p.Has("teacher") && p.UserID == scope.TeacherID)
}

func sourceActions(p identity.Principal, scope sessionScope, source Source) []string {
	actions := []string{}
	if canVerify(p, scope) {
		actions = append(actions, "verify")
	}
	if canUpload(p, scope) && scope.Status != "deleting" && scope.Status != "deleted" {
		actions = append(actions, "upload")
	}
	sort.Strings(actions)
	return actions
}

func mediaActions(p identity.Principal, scope sessionScope, item Media, rights string, allowed []string, playbackAvailable bool) []string {
	actions := []string{}
	if item.Status == "pending" && rights == "verified" && contains(allowed, "playback") && canUpload(p, scope) {
		actions = append(actions, "prepare_media")
	}
	if (item.Status == "pending" || item.Status == "ready") && rights == "verified" && contains(allowed, "analysis") && canUpload(p, scope) {
		actions = append(actions, "analyze")
	}
	if playbackAvailable && item.Status == "ready" && rights == "verified" && contains(allowed, "playback") && canViewContent(p, scope) && item.ExpiresAt != nil && item.ExpiresAt.After(time.Now()) && scope.ContentExpiresAt.After(time.Now()) {
		actions = append(actions, "playback")
	}
	if playbackAvailable && item.Kind == "source" && item.Status == "ready" && item.PlaybackAssetID != nil && canUpload(p, scope) {
		actions = append(actions, "select_primary")
	}
	sort.Strings(actions)
	return actions
}

func setPlaybackURL(item *Media, scope sessionScope, rights string, allowed []string, playbackAvailable bool, now time.Time) {
	if !playbackAvailable || item.Status != "ready" || rights != "verified" || !contains(allowed, "playback") || item.ExpiresAt == nil || !item.ExpiresAt.After(now) || !scope.ContentExpiresAt.After(now) {
		return
	}
	target := item.ID
	if item.Kind == "source" {
		if item.PlaybackAssetID == nil {
			return
		}
		target = *item.PlaybackAssetID
	} else if !oneOf(item.Kind, "proxy", "audio", "keyframe") {
		return
	}
	value := "/api/v1/media/" + target + "/content"
	item.PlaybackURL = &value
}

func validateUses(values []string) ([]string, error) {
	if values == nil {
		return nil, invalid("uses are required")
	}
	set := map[string]bool{}
	for _, value := range values {
		if !oneOf(value, "playback", "analysis", "export") || set[value] {
			return nil, invalid("uses must be unique playback, analysis, or export values")
		}
		set[value] = true
	}
	result := make([]string, len(values))
	copy(result, values)
	sort.Strings(result)
	return result, nil
}

func subset(values, requested []string) bool {
	for _, value := range values {
		if !contains(requested, value) {
			return false
		}
	}
	return true
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func oneOf(value string, values ...string) bool { return contains(values, value) }

func mediaType(filename, declared string) (string, string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	declared = strings.ToLower(strings.TrimSpace(strings.Split(declared, ";")[0]))
	allowed := map[string]string{".mp4": "video/mp4", ".mov": "video/quicktime", ".mkv": "video/x-matroska"}
	wanted, ok := allowed[ext]
	if !ok || (declared != "" && declared != "application/octet-stream" && declared != wanted && !(ext == ".mkv" && declared == "video/matroska")) {
		return "", "", apperror.New(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA", "only MP4, MOV, and MKV uploads are accepted")
	}
	return wanted, ext, nil
}

func randomObjectKey(sessionID, extension string) (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate media object key: %w", err)
	}
	return filepath.ToSlash(filepath.Join("sessions", sessionID, "source", hex.EncodeToString(data)+extension)), nil
}

func validContainer(extension string, header []byte) bool {
	if extension == ".mkv" {
		return len(header) >= 4 && header[0] == 0x1a && header[1] == 0x45 && header[2] == 0xdf && header[3] == 0xa3
	}
	return len(header) >= 12 && string(header[4:8]) == "ftyp"
}

func paginateSources(items []Source, limit int) ([]Source, string, error) {
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func invalid(message string) error {
	return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", message)
}
func forbidden(message string) error { return apperror.New(http.StatusForbidden, "FORBIDDEN", message) }
func state(message string) error     { return apperror.New(http.StatusConflict, "INVALID_STATE", message) }
func notFound(resource string) error {
	return apperror.New(http.StatusNotFound, "NOT_FOUND", resource+" not found")
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503", "23514", "22P02":
			return invalid("related resource or field is invalid")
		case "23505":
			return state("resource conflicts with existing data")
		}
	}
	return fmt.Errorf("media storage: %w", err)
}
