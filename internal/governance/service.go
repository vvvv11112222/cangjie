package governance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

type Config struct {
	Lease, Recheck, ExportRetention time.Duration
	MaxAttempts                     int
	ExecutorID                      string
	BudgetCurrency                  string
	BudgetMonthlyMicros             int64
	WorkerTempRoot                  string
	AuditRetention                  time.Duration
}

type Service struct {
	// Set before Run. Deletion cannot complete while capture writers remain active.
	BeforeCaptureCleanup func(context.Context, string) error
	pool                 *pgxpool.Pool
	store                storage.Backend
	ledger               *Ledger
	cfg                  Config
	now                  func() time.Time
	commitExportTx       func(context.Context, pgx.Tx) error
}

type CreateExport struct {
	Format string `json:"format"`
}
type Export struct {
	ID, ReportID, Status string
	ErrorCode            *string
	DownloadURL          *string
	ExpiresAt            time.Time
}

func (e Export) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID          string    `json:"id"`
		ReportID    string    `json:"report_id"`
		Status      string    `json:"status"`
		ErrorCode   *string   `json:"error_code"`
		DownloadURL *string   `json:"download_url"`
		ExpiresAt   time.Time `json:"expires_at"`
	}{e.ID, e.ReportID, e.Status, e.ErrorCode, e.DownloadURL, e.ExpiresAt})
}

type Deletion struct {
	ID, SessionID, Status string
	ErrorCode             *string
	CompletedAt           *time.Time
}

func (d Deletion) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID          string     `json:"id"`
		SessionID   string     `json:"session_id"`
		Status      string     `json:"status"`
		ErrorCode   *string    `json:"error_code"`
		CompletedAt *time.Time `json:"completed_at"`
	}{d.ID, d.SessionID, d.Status, d.ErrorCode, d.CompletedAt})
}

type Content struct {
	File     storage.ReadSeekCloser
	Size     int64
	MIMEType string
	Filename string
}

type Worker struct {
	WorkerID     string    `json:"worker_id"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	Capabilities []string  `json:"capabilities"`
}
type WorkerCleanup struct {
	DeletionRequestID string   `json:"deletion_request_id"`
	SessionID         string   `json:"session_id"`
	StorageGeneration int64    `json:"storage_generation"`
	JobIDs            []string `json:"job_ids"`
}
type Budget struct{ Currency, BillingPeriod, Limit, Charged, Reserved, Unknown string }

func (b Budget) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Currency      string `json:"currency"`
		BillingPeriod string `json:"billing_period"`
		Limit         string `json:"limit"`
		Charged       string `json:"charged"`
		Reserved      string `json:"reserved"`
		Unknown       string `json:"unknown"`
	}{b.Currency, b.BillingPeriod, b.Limit, b.Charged, b.Reserved, b.Unknown})
}

type Operations struct {
	Workers          []Worker `json:"workers"`
	QueuedJobs       int      `json:"queued_jobs"`
	StorageFreeBytes int64    `json:"storage_free_bytes"`
	Budget           Budget   `json:"budget"`
}

func NewService(pool *pgxpool.Pool, store storage.Backend, ledger *Ledger, cfg Config) *Service {
	if cfg.Lease <= 0 {
		cfg.Lease = 2 * time.Minute
	}
	if cfg.Recheck <= 0 {
		cfg.Recheck = time.Minute
	}
	if cfg.ExportRetention <= 0 {
		cfg.ExportRetention = 24 * time.Hour
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.ExecutorID == "" {
		cfg.ExecutorID = "governance"
	}
	if cfg.BudgetCurrency == "" {
		cfg.BudgetCurrency = "CNY"
	}
	if cfg.AuditRetention <= 0 {
		cfg.AuditRetention = 180 * 24 * time.Hour
	}
	return &Service{pool: pool, store: store, ledger: ledger, cfg: cfg, now: time.Now}
}

func digest(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func forbidden() error {
	return apperror.New(http.StatusForbidden, "FORBIDDEN", "operation is not allowed")
}
func notFound() error { return apperror.New(http.StatusNotFound, "NOT_FOUND", "resource not found") }
func invalid(message string) error {
	return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", message)
}
func conflict(code, message string) error { return apperror.New(http.StatusConflict, code, message) }

func canView(p identity.Principal, teacher, college string) bool {
	return p.Has("sys_admin") || p.Scoped("supervisor", college) || (p.Has("teacher") && p.UserID == teacher)
}

func scanExport(row pgx.Row) (Export, error) {
	var value Export
	err := row.Scan(&value.ID, &value.ReportID, &value.Status, &value.ErrorCode, &value.ExpiresAt)
	return value, err
}

func (s *Service) CreateReportExport(ctx context.Context, p identity.Principal, reportID, key string, in CreateExport) (Export, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 || (in.Format != "json" && in.Format != "pdf") {
		return Export{}, false, invalid("format and Idempotency-Key are required")
	}
	requestHash := digest(in)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Export{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var runID, sessionID, status, sessionStatus, teacher, college, rights string
	var uses []byte
	var reportExpiry, contentExpiry time.Time
	var generation int64
	err = tx.QueryRow(ctx, `SELECT r.run_id::text,r.session_id::text,r.status,r.expires_at,ls.content_expires_at,ls.status,
		o.teacher_id::text,teaching.college_of(o.org_unit_id)::text,src.rights_status,src.allowed_uses,ls.storage_generation
		FROM teaching.reports r JOIN teaching.analysis_runs ar ON ar.id=r.run_id JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		JOIN teaching.course_offerings o ON o.id=ls.offering_id JOIN teaching.media_assets m ON m.id=ar.media_asset_id
		JOIN teaching.source_records src ON src.id=m.source_record_id WHERE r.id=$1 FOR UPDATE OF ls,r`, reportID).
		Scan(&runID, &sessionID, &status, &reportExpiry, &contentExpiry, &sessionStatus, &teacher, &college, &rights, &uses, &generation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canView(p, teacher, college) {
		return Export{}, false, notFound()
	}
	if err != nil {
		return Export{}, false, err
	}
	if status != "published" || sessionStatus == "deleting" || sessionStatus == "deleted" {
		return Export{}, false, conflict("INVALID_STATE", "only the current published report can be exported")
	}
	var current bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM teaching.reports newer WHERE newer.session_id=$1 AND newer.status='published' AND newer.id<>$2)`, sessionID, reportID).Scan(&current); err != nil {
		return Export{}, false, err
	}
	if !current || !reportExpiry.After(s.now()) || !contentExpiry.After(s.now()) || rights != "verified" || !jsonArrayHas(uses, "export") {
		return Export{}, false, apperror.New(http.StatusGone, "RESOURCE_EXPIRED", "report export is not currently permitted")
	}
	if existing, scanErr := scanExport(tx.QueryRow(ctx, `SELECT id::text,report_id::text,status,error_code,expires_at FROM teaching.export_jobs WHERE requested_by=$1 AND report_id=$2 AND idempotency_key=$3`, p.UserID, reportID, key)); scanErr == nil {
		var saved string
		_ = tx.QueryRow(ctx, `SELECT request_sha256 FROM teaching.export_jobs WHERE id=$1`, existing.ID).Scan(&saved)
		if saved != requestHash {
			return Export{}, false, conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key was used with different parameters")
		}
		return existing, true, nil
	} else if !errors.Is(scanErr, pgx.ErrNoRows) {
		return Export{}, false, scanErr
	}
	expires := s.now().Add(s.cfg.ExportRetention)
	if reportExpiry.Before(expires) {
		expires = reportExpiry
	}
	if contentExpiry.Before(expires) {
		expires = contentExpiry
	}
	value, err := scanExport(tx.QueryRow(ctx, `INSERT INTO teaching.export_jobs(report_id,run_id,session_id,requested_by,format,idempotency_key,request_sha256,expires_at,storage_generation)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text,report_id::text,status,error_code,expires_at`, reportID, runID, sessionID, p.UserID, in.Format, key, requestHash, expires, generation))
	if err != nil {
		return Export{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata) VALUES($1,'report.export.request','report_export',$2,'success','service',jsonb_build_object('format',$3::text,'report_id',$4::text))`, p.UserID, value.ID, in.Format, reportID)
	if err != nil {
		return Export{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Export{}, false, err
	}
	return value, false, nil
}

func jsonArrayHas(raw []byte, want string) bool {
	var values []string
	_ = json.Unmarshal(raw, &values)
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func (s *Service) exportAccess(ctx context.Context, p identity.Principal, id string, forDownload bool) (Export, *string, *string, *int64, error) {
	var value Export
	var requester, format, teacher, college, sessionStatus, reportStatus, rights string
	var uses []byte
	var objectKey, mime *string
	var size *int64
	var reportExpiry, contentExpiry time.Time
	err := s.pool.QueryRow(ctx, `SELECT e.id::text,e.report_id::text,e.status,e.error_code,e.expires_at,e.requested_by::text,e.format,
		o.teacher_id::text,teaching.college_of(o.org_unit_id)::text,ls.status,r.status,r.expires_at,ls.content_expires_at,
		src.rights_status,src.allowed_uses,m.object_key,m.mime_type,m.byte_size
		FROM teaching.export_jobs e JOIN teaching.reports r ON r.id=e.report_id JOIN teaching.analysis_runs ar ON ar.id=e.run_id
		JOIN teaching.lesson_sessions ls ON ls.id=e.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id
		JOIN teaching.media_assets source ON source.id=ar.media_asset_id JOIN teaching.source_records src ON src.id=source.source_record_id
		LEFT JOIN teaching.media_assets m ON m.id=e.output_asset_id WHERE e.id=$1`, id).
		Scan(&value.ID, &value.ReportID, &value.Status, &value.ErrorCode, &value.ExpiresAt, &requester, &format, &teacher, &college, &sessionStatus, &reportStatus, &reportExpiry, &contentExpiry, &rights, &uses, &objectKey, &mime, &size)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (requester != p.UserID || !canView(p, teacher, college)) {
		return Export{}, nil, nil, nil, notFound()
	}
	if err != nil {
		return Export{}, nil, nil, nil, err
	}
	valid := sessionStatus != "deleting" && sessionStatus != "deleted" && reportStatus == "published" && value.ExpiresAt.After(s.now()) && reportExpiry.After(s.now()) && contentExpiry.After(s.now()) && rights == "verified" && jsonArrayHas(uses, "export")
	if value.Status == "succeeded" && valid {
		url := "/api/v1/exports/" + value.ID + "/content"
		value.DownloadURL = &url
	}
	if forDownload && (value.DownloadURL == nil || objectKey == nil || mime == nil || size == nil) {
		return Export{}, nil, nil, nil, apperror.New(http.StatusGone, "RESOURCE_EXPIRED", "export is no longer downloadable")
	}
	return value, objectKey, mime, size, nil
}

func (s *Service) GetExport(ctx context.Context, p identity.Principal, id string) (Export, error) {
	v, _, _, _, e := s.exportAccess(ctx, p, id, false)
	return v, e
}
func (s *Service) OpenExport(ctx context.Context, p identity.Principal, id string) (Content, error) {
	v, key, mime, size, err := s.exportAccess(ctx, p, id, true)
	if err != nil {
		return Content{}, err
	}
	file, err := s.store.Open(*key)
	if err != nil {
		return Content{}, err
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata) VALUES($1,'report.export.download','report_export',$2,'success','service',jsonb_build_object('report_id',$3::text))`, p.UserID, id, v.ReportID); err != nil {
		file.Close() //nolint:errcheck
		return Content{}, err
	}
	ext := "json"
	if *mime == "application/pdf" {
		ext = "pdf"
	}
	return Content{File: file, Size: *size, MIMEType: *mime, Filename: "report-" + v.ReportID + "." + ext}, nil
}

type exportSnapshot struct {
	SchemaVersion        string            `json:"schema_version"`
	ExportedAt           time.Time         `json:"exported_at"`
	Report               map[string]any    `json:"report"`
	Source               map[string]any    `json:"source"`
	EvidenceAvailability map[string]string `json:"evidence_availability"`
}

func (s *Service) buildExport(ctx context.Context, reportID string) (exportSnapshot, error) {
	var snap exportSnapshot
	snap.SchemaVersion = "1.1"
	snap.ExportedAt = s.now().UTC()
	var reportRaw, sourceRaw []byte
	err := s.pool.QueryRow(ctx, `SELECT jsonb_build_object('id',r.id,'session_id',r.session_id,'revision',r.revision,'status',r.status,'summary',r.summary,
		'summary_evidence_ids',r.summary_evidence_ids,'limitations',r.limitations,'published_at',r.published_at,'content_sha256',r.content_sha256,'provenance',r.provenance,
		'dimensions',COALESCE((SELECT jsonb_agg(jsonb_build_object('dimension_code',d.dimension_code,'coverage_status',d.coverage_status,'summary',d.summary,'limitation',d.limitation,'coverage',d.coverage,'summary_evidence_ids',d.summary_evidence_ids) ORDER BY array_position(ARRAY['content','pace','thinking','expression','management','technology'],d.dimension_code)) FROM teaching.report_dimensions d WHERE d.report_id=r.id),'[]'::jsonb),
		'observations',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',ro.id,'dimension_code',ro.dimension_code,'observation_type',ro.observation_type,'observation_text',ro.observation_text,'suggestion',ro.suggestion,'review_status',ro.review_status,'evidence_ids',COALESCE((SELECT jsonb_agg(oe.evidence_id ORDER BY oe.evidence_id) FROM teaching.observation_evidence oe WHERE oe.observation_id=ro.id),'[]'::jsonb)) ORDER BY ro.sort_order,ro.id) FROM teaching.report_observations ro WHERE ro.report_id=r.id AND ro.removed_at IS NULL AND ro.review_status<>'rejected'),'[]'::jsonb)),
		jsonb_build_object('title',src.title,'source_type',src.source_type,'attribution',src.attribution,'license_name',src.license_name,'license_url',src.license_url,'rights_status',src.rights_status,'allowed_uses',src.allowed_uses)
		FROM teaching.reports r JOIN teaching.analysis_runs ar ON ar.id=r.run_id JOIN teaching.media_assets m ON m.id=ar.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id WHERE r.id=$1`, reportID).Scan(&reportRaw, &sourceRaw)
	if err != nil {
		return snap, err
	}
	if err = json.Unmarshal(reportRaw, &snap.Report); err != nil {
		return snap, err
	}
	if err = json.Unmarshal(sourceRaw, &snap.Source); err != nil {
		return snap, err
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id::text,e.availability FROM teaching.evidence_items e JOIN teaching.reports r ON r.run_id=e.run_id WHERE r.id=$1 ORDER BY e.id`, reportID)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	snap.EvidenceAvailability = map[string]string{}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			return snap, err
		}
		snap.EvidenceAvailability[id] = state
	}
	return snap, rows.Err()
}

func (s *Service) ProcessExport(ctx context.Context) (bool, error) {
	var id, reportID, format, token string
	var generation int64
	err := s.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM teaching.export_jobs WHERE (status='queued' AND available_at<=now()) OR (status='running' AND lease_expires_at<=now()) ORDER BY available_at,created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		UPDATE teaching.export_jobs e SET status='running',executor_id=$1,lease_token=gen_random_uuid(),lease_expires_at=now()+$2::interval,attempts=attempts+1,updated_at=now() FROM candidate c WHERE e.id=c.id RETURNING e.id::text,e.report_id::text,e.format,e.lease_token::text,e.storage_generation`, s.cfg.ExecutorID, s.cfg.Lease.String()).Scan(&id, &reportID, &format, &token, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	snap, err := s.buildExport(ctx, reportID)
	if err != nil {
		return true, s.failExport(ctx, id, token, err)
	}
	var body []byte
	mime := "application/json"
	if format == "pdf" {
		body = renderPDF(snap)
		mime = "application/pdf"
	} else {
		body, err = json.MarshalIndent(snap, "", "  ")
		if err == nil {
			body = append(body, '\n')
		}
	}
	if err != nil {
		return true, s.failExport(ctx, id, token, err)
	}
	staged, err := s.store.Stage(ctx, bytes.NewReader(body), int64(len(body))+1)
	if err != nil {
		return true, s.failExport(ctx, id, token, err)
	}
	defer s.store.Discard(staged) //nolint:errcheck
	// Every lease writes to its own immutable key. A stale executor can then
	// only remove its own attempt and can never clobber the winning object.
	key := "exports/" + id + "/" + token + "." + format
	if err = s.store.Commit(staged, key); err != nil {
		return true, s.failExport(ctx, id, token, err)
	}
	removeOnFailure := true
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		s.store.Remove(key)
		return true, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var sessionID, status, reportStatus, rights string
	var currentGeneration int64
	var expiry time.Time
	var uses []byte
	err = tx.QueryRow(ctx, `SELECT e.session_id::text,e.status,e.expires_at,ls.storage_generation,ls.status,r.status,src.rights_status,src.allowed_uses FROM teaching.export_jobs e JOIN teaching.reports r ON r.id=e.report_id JOIN teaching.analysis_runs ar ON ar.id=e.run_id JOIN teaching.lesson_sessions ls ON ls.id=e.session_id JOIN teaching.media_assets m ON m.id=ar.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id WHERE e.id=$1 AND e.lease_token=$2::uuid FOR UPDATE OF e,ls`, id, token).Scan(&sessionID, &status, &expiry, &currentGeneration, new(string), &reportStatus, &rights, &uses)
	if err != nil || status != "running" || generation != currentGeneration || !expiry.After(s.now()) || reportStatus != "published" || rights != "verified" || !jsonArrayHas(uses, "export") {
		s.store.Remove(key)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE teaching.export_jobs SET status='expired',executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1`, id)
			if err == nil {
				err = tx.Commit(ctx)
			}
		}
		return true, err
	}
	var assetID string
	err = tx.QueryRow(ctx, `INSERT INTO teaching.media_assets(session_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,status,expires_at) VALUES($1,'report_export','filesystem',$2,$3,$4,$5,'ready',$6) RETURNING id::text`, sessionID, key, staged.SHA256, mime, staged.Size, expiry).Scan(&assetID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE teaching.export_jobs SET status='succeeded',output_asset_id=$2,mime_type=$3,byte_size=$4,sha256=$5,executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1`, id, assetID, mime, staged.Size, staged.SHA256)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(action,resource_type,resource_id,result,request_id,metadata) VALUES('report.export.complete','report_export',$1,'success','governance',jsonb_build_object('report_id',$2::text,'sha256',$3::text))`, id, reportID, staged.SHA256)
	}
	if err == nil {
		// Once COMMIT is sent its result may be uncertain. Preserve the object so
		// a transaction that actually committed never points at deleted bytes.
		removeOnFailure = false
		if s.commitExportTx != nil {
			err = s.commitExportTx(ctx, tx)
		} else {
			err = tx.Commit(ctx)
		}
		// A PostgreSQL rejection followed by idle transaction status confirms
		// rollback. Transport errors do not establish the commit outcome.
		var serverError *pgconn.PgError
		removeOnFailure = errors.Is(err, pgx.ErrTxCommitRollback) ||
			(errors.As(err, &serverError) && serverError.Severity == "ERROR" && tx.Conn().PgConn().TxStatus() == 'I')
	}
	if err != nil && removeOnFailure {
		s.store.Remove(key)
	}
	return true, err
}

func (s *Service) failExport(ctx context.Context, id, token string, cause error) error {
	_, err := s.pool.Exec(ctx, `UPDATE teaching.export_jobs SET status=CASE WHEN attempts>=$3 THEN 'failed' ELSE 'queued' END,error_code='EXPORT_FAILED',available_at=now()+$4::interval,executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2::uuid`, id, token, s.cfg.MaxAttempts, s.cfg.Recheck.String())
	if err != nil {
		return err
	}
	return cause
}

func renderPDF(s exportSnapshot) []byte {
	lines := pdfLines(s)
	const linesPerPage = 44
	pages := make([][]string, 0, (len(lines)+linesPerPage-1)/linesPerPage)
	for len(lines) > 0 {
		count := min(linesPerPage, len(lines))
		if count < len(lines) {
			for i := count - 1; i > 0; i-- {
				if strings.HasPrefix(lines[i], "维度/状态: ") || strings.HasPrefix(lines[i], "观察/复核: ") {
					count = i
					break
				}
			}
		}
		pages = append(pages, lines[:count])
		lines = lines[count:]
	}
	if len(pages) == 0 {
		pages = append(pages, []string{"教学质量报告"})
	}
	fontObject := 3 + len(pages)*2
	kids := make([]string, 0, len(pages))
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i*2))
	}
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))}
	for i, pageLines := range pages {
		content := ""
		contentY := 810
		if i > 0 {
			content = "BT /F1 9 Tf 42 820 Td <" + utf16Hex("教学质量报告（续）") + "> Tj ET "
			contentY = 794
		}
		content += fmt.Sprintf("BT /F1 11 Tf 42 %d Td 16 TL ", contentY)
		for _, line := range pageLines {
			content += "<" + utf16Hex(line) + "> Tj T* "
		}
		content += "ET BT /F1 9 Tf 260 28 Td <" + utf16Hex(fmt.Sprintf("第 %d / %d 页", i+1, len(pages))) + "> Tj ET"
		contentObject := 4 + i*2
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontObject, contentObject),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	objects = append(objects,
		fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [%d 0 R] >>", fontObject+1),
		fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 4 >> /DW 1000 /W [1 95 500] /FontDescriptor %d 0 R >>", fontObject+2),
		"<< /Type /FontDescriptor /FontName /STSong-Light /Flags 6 /FontBBox [-25 -254 1000 880] /ItalicAngle 0 /Ascent 752 /Descent -271 /CapHeight 737 /StemV 58 /MissingWidth 1000 >>")
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, o := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

func pdfLines(s exportSnapshot) []string {
	lines := []string{"教学质量报告", "报告ID: " + fmt.Sprint(s.Report["id"]), "版本: " + fmt.Sprint(s.Report["revision"]), "导出时间: " + s.ExportedAt.Format(time.RFC3339)}
	appendWrapped := func(label string, value any) {
		if value == nil {
			value = "未提供"
		}
		text := label + fmt.Sprint(value)
		for len([]rune(text)) > 42 {
			runes := []rune(text)
			lines = append(lines, string(runes[:42]))
			text = "  " + string(runes[42:])
		}
		lines = append(lines, text)
	}
	appendWrapped("总摘要: ", s.Report["summary"])
	appendWrapped("总摘要证据: ", s.Report["summary_evidence_ids"])
	appendWrapped("报告限制: ", s.Report["limitations"])
	for _, field := range []struct{ key, label string }{
		{"title", "来源标题"}, {"attribution", "来源署名"}, {"source_type", "来源类型"},
		{"license_name", "许可名称"}, {"license_url", "许可链接"}, {"rights_status", "核验状态"}, {"allowed_uses", "授权用途"},
	} {
		if value := s.Source[field.key]; value != nil {
			appendWrapped(field.label+": ", value)
		}
	}
	appendWrapped("内容摘要 SHA-256: ", s.Report["content_sha256"])
	if provenance := s.Report["provenance"]; provenance != nil {
		// JSON provides deterministic map order and preserves nested versions,
		// hashes, coverage and limitations from this fixed export snapshot.
		raw, _ := json.Marshal(provenance)
		appendWrapped("执行溯源: ", string(raw))
	}
	evidenceIDs := make([]string, 0, len(s.EvidenceAvailability))
	for id := range s.EvidenceAvailability {
		evidenceIDs = append(evidenceIDs, id)
	}
	sort.Strings(evidenceIDs)
	for _, id := range evidenceIDs {
		appendWrapped("证据可用性: ", id+" / "+s.EvidenceAvailability[id])
	}
	for _, raw := range anySlice(s.Report["dimensions"]) {
		dimension, _ := raw.(map[string]any)
		appendWrapped("维度/状态: ", fmt.Sprintf("%v / %v", dimension["dimension_code"], dimension["coverage_status"]))
		appendWrapped("维度结论: ", dimension["summary"])
		appendWrapped("不足原因: ", dimension["limitation"])
		appendWrapped("维度证据: ", dimension["summary_evidence_ids"])
		appendWrapped("维度覆盖: ", dimension["coverage"])
	}
	for _, raw := range anySlice(s.Report["observations"]) {
		observation, _ := raw.(map[string]any)
		appendWrapped("观察/复核: ", fmt.Sprintf("%v / %v", observation["dimension_code"], observation["review_status"]))
		appendWrapped("观察内容: ", observation["observation_text"])
		appendWrapped("改进建议: ", observation["suggestion"])
		appendWrapped("观察证据: ", observation["evidence_ids"])
	}
	return lines
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}
func utf16Hex(value string) string {
	units := utf16.Encode([]rune(value))
	var b strings.Builder
	for _, u := range units {
		fmt.Fprintf(&b, "%04X", u)
	}
	return b.String()
}

func scanDeletion(row pgx.Row) (Deletion, error) {
	var d Deletion
	err := row.Scan(&d.ID, &d.SessionID, &d.Status, &d.ErrorCode, &d.CompletedAt)
	return d, err
}

func (s *Service) CreateDeletion(ctx context.Context, p identity.Principal, sessionID, key, reason string) (Deletion, bool, error) {
	if !p.Has("sys_admin") {
		return Deletion{}, false, forbidden()
	}
	key = strings.TrimSpace(key)
	reason = strings.TrimSpace(reason)
	if key == "" || len(key) > 128 || reason == "" {
		return Deletion{}, false, invalid("reason and Idempotency-Key are required")
	}
	hash := digest(map[string]string{"reason": reason})
	if old, err := scanDeletion(s.pool.QueryRow(ctx, `SELECT id::text,session_id::text,status,error_code,completed_at FROM teaching.data_deletion_requests WHERE requested_by=$1 AND session_id=$2 AND idempotency_key=$3`, p.UserID, sessionID, key)); err == nil {
		var saved string
		_ = s.pool.QueryRow(ctx, `SELECT request_sha256 FROM teaching.data_deletion_requests WHERE id=$1`, old.ID).Scan(&saved)
		if saved != hash {
			return Deletion{}, false, conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key was used with different parameters")
		}
		return old, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Deletion{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deletion{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, sessionID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return Deletion{}, false, notFound()
	} else if err != nil {
		return Deletion{}, false, err
	}
	if status == "deleted" {
		return Deletion{}, false, conflict("INVALID_STATE", "classroom is already deleted")
	}
	if old, scanErr := scanDeletion(tx.QueryRow(ctx, `SELECT id::text,session_id::text,status,error_code,completed_at FROM teaching.data_deletion_requests WHERE requested_by=$1 AND session_id=$2 AND idempotency_key=$3`, p.UserID, sessionID, key)); scanErr == nil {
		var saved string
		if err = tx.QueryRow(ctx, `SELECT request_sha256 FROM teaching.data_deletion_requests WHERE id=$1`, old.ID).Scan(&saved); err != nil {
			return Deletion{}, false, err
		}
		if saved != hash {
			return Deletion{}, false, conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key was used with different parameters")
		}
		return old, true, nil
	} else if !errors.Is(scanErr, pgx.ErrNoRows) {
		return Deletion{}, false, scanErr
	}
	tombstone, err := newUUID()
	if err != nil {
		return Deletion{}, false, err
	}
	entry := Tombstone{TombstoneID: tombstone, SessionID: sessionID, RequestedBy: p.UserID, Reason: reason, IdempotencyKey: key, RequestSHA256: hash, RequestedAt: s.now().UTC()}
	if err := s.ledger.Append(entry); err != nil {
		return Deletion{}, false, fmt.Errorf("persist deletion tombstone: %w", err)
	}
	var requiredWorkers []string
	if err = tx.QueryRow(ctx, `SELECT CASE
		WHEN NOT EXISTS(SELECT 1 FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id WHERE r.session_id=$1 AND j.stage IN('probe','audio_analysis','video_analysis'))
		THEN '{}'::text[]
		WHEN NOT EXISTS(SELECT 1 FROM teaching.governance_workers)
		THEN ARRAY['__unverified_worker_history__']::text[]
		ELSE COALESCE((SELECT array_agg(worker_id ORDER BY worker_id) FROM teaching.governance_workers),'{}'::text[]) END`, sessionID).Scan(&requiredWorkers); err != nil {
		return Deletion{}, false, err
	}
	value, err := scanDeletion(tx.QueryRow(ctx, `INSERT INTO teaching.data_deletion_requests(session_id,requested_by,reason,idempotency_key,request_sha256,tombstone_id,required_worker_ids) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text,session_id::text,status,error_code,completed_at`, sessionID, p.UserID, reason, key, hash, tombstone, requiredWorkers))
	if err != nil {
		return Deletion{}, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.lesson_sessions SET status='deleting',storage_generation=storage_generation+1 WHERE id=$1`, sessionID); err != nil {
		return Deletion{}, false, err
	}
	if err = revokeDeletionWork(ctx, tx, sessionID); err != nil {
		return Deletion{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata) VALUES($1,'classroom.delete.request','lesson_session',$2,'success','service',jsonb_build_object('tombstone_id',$3::text))`, p.UserID, sessionID, tombstone)
	if err != nil {
		return Deletion{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Deletion{}, false, err
	}
	return value, false, nil
}

// The caller holds the classroom lock. Both normal deletion and ledger replay
// transfer billing responsibility before unlinking the run or cancelling jobs.
func revokeDeletionWork(ctx context.Context, tx pgx.Tx, sessionID string) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT currency||':'||billing_period::text FROM teaching.model_calls
		WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1) AND status='reserved' ORDER BY 1`, sessionID)
	if err != nil {
		return err
	}
	var periods []string
	for rows.Next() {
		var period string
		if err = rows.Scan(&period); err != nil {
			break
		}
		periods = append(periods, period)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, period := range periods {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, period); err != nil {
			return err
		}
	}
	statements := []string{
		`UPDATE teaching.model_calls SET status='unknown',lock_version=lock_version+1 WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1) AND status='reserved' AND dispatch_started_at IS NOT NULL`,
		`UPDATE teaching.model_calls SET status='failed',actual_cost=0,settled_at=now(),lock_version=lock_version+1 WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1) AND status='reserved' AND dispatch_started_at IS NULL`,
		`UPDATE teaching.analysis_jobs SET status='cancelled',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code='RESOURCE_DELETING',updated_at=now() WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1) AND status IN('queued','running')`,
		`UPDATE teaching.analysis_runs SET status='cancelled',error_code='RESOURCE_DELETING',finished_at=now() WHERE session_id=$1 AND status IN('queued','running')`,
		`UPDATE teaching.export_jobs SET status='expired',executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE session_id=$1 AND status IN('queued','running','succeeded')`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, sessionID); err != nil {
			return err
		}
	}
	return nil
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate tombstone id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (s *Service) GetDeletion(ctx context.Context, p identity.Principal, id string) (Deletion, error) {
	if !p.Has("sys_admin") {
		return Deletion{}, forbidden()
	}
	v, err := scanDeletion(s.pool.QueryRow(ctx, `SELECT id::text,session_id::text,status,error_code,completed_at FROM teaching.data_deletion_requests WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deletion{}, notFound()
	}
	return v, err
}

func (s *Service) ReplayLedger(ctx context.Context) error {
	entries, err := s.ledger.ReadAll(ctx)
	if err != nil {
		return err
	}
	ledgerIDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		ledgerIDs[entry.TombstoneID] = true
	}
	rows, err := s.pool.Query(ctx, `SELECT tombstone_id::text FROM teaching.data_deletion_requests`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tombstoneID string
		if err = rows.Scan(&tombstoneID); err != nil {
			rows.Close()
			return err
		}
		if !ledgerIDs[tombstoneID] {
			rows.Close()
			return fmt.Errorf("database deletion tombstone %s is missing from the independent ledger", tombstoneID)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, e := range entries {
		tx, beginErr := s.pool.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			return beginErr
		}
		var exists bool
		var sessionStatus string
		beginErr = tx.QueryRow(ctx, `SELECT status FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, e.SessionID).Scan(&sessionStatus)
		if beginErr == nil {
			beginErr = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.data_deletion_requests WHERE tombstone_id=$1)`, e.TombstoneID).Scan(&exists)
		}
		if beginErr == nil && !exists {
			_, beginErr = tx.Exec(ctx, `UPDATE teaching.lesson_sessions SET status='deleting',storage_generation=storage_generation+1 WHERE id=$1 AND status NOT IN('deleting','deleted')`, e.SessionID)
			if beginErr == nil {
				_, beginErr = tx.Exec(ctx, `INSERT INTO teaching.data_deletion_requests(session_id,requested_by,reason,idempotency_key,request_sha256,tombstone_id,required_worker_ids)
					VALUES($1,$2,$3,$4,$5,$6,CASE
						WHEN NOT EXISTS(SELECT 1 FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id WHERE r.session_id=$1 AND j.stage IN('probe','audio_analysis','video_analysis'))
						THEN '{}'::text[]
						WHEN NOT EXISTS(SELECT 1 FROM teaching.governance_workers)
						THEN ARRAY['__unverified_worker_history__']::text[]
						ELSE COALESCE((SELECT array_agg(worker_id ORDER BY worker_id) FROM teaching.governance_workers),'{}'::text[]) END)
					ON CONFLICT(requested_by,session_id,idempotency_key) DO NOTHING`, e.SessionID, e.RequestedBy, e.Reason, e.IdempotencyKey, e.RequestSHA256, e.TombstoneID)
			}
		}
		if beginErr == nil && sessionStatus != "deleted" {
			beginErr = revokeDeletionWork(ctx, tx, e.SessionID)
		}
		if beginErr == nil {
			beginErr = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if beginErr != nil {
			return fmt.Errorf("replay tombstone %s: %w", e.TombstoneID, beginErr)
		}
	}
	return nil
}

func (s *Service) ProcessDeletion(ctx context.Context) (bool, error) {
	var id, sessionID, token string
	err := s.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM teaching.data_deletion_requests WHERE (status IN('requested','blocked') AND available_at<=now()) OR (status='processing' AND lease_expires_at<=now()) ORDER BY available_at,created_at LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE teaching.data_deletion_requests d SET status='processing',executor_id=$1,lease_token=gen_random_uuid(),lease_expires_at=now()+$2::interval,
		attempts=attempts+CASE WHEN d.status='blocked' AND d.error_code IN('WORKER_CLEANUP_PENDING','CAPTURE_CLEANUP_PENDING') THEN 0 ELSE 1 END,updated_at=now()
		FROM candidate c WHERE d.id=c.id RETURNING d.id::text,d.session_id::text,d.lease_token::text`, s.cfg.ExecutorID, s.cfg.Lease.String()).Scan(&id, &sessionID, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var hasCaptures bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.live_captures WHERE session_id=$1)`, sessionID).Scan(&hasCaptures); err != nil {
		return true, s.failDeletion(ctx, id, token, "CAPTURE_CLEANUP_CHECK_FAILED")
	}
	if hasCaptures {
		if s.BeforeCaptureCleanup == nil {
			return true, s.blockDeletion(ctx, id, token, "CAPTURE_CLEANUP_PENDING")
		}
		if err = s.BeforeCaptureCleanup(ctx, sessionID); err != nil {
			return true, s.blockDeletion(ctx, id, token, "CAPTURE_CLEANUP_PENDING")
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT object_key FROM teaching.media_assets WHERE session_id=$1 AND status<>'deleted'`, sessionID)
	if err != nil {
		return true, s.failDeletion(ctx, id, token, "STORAGE_SCAN_FAILED")
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return true, s.failDeletion(ctx, id, token, "STORAGE_SCAN_FAILED")
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return true, s.failDeletion(ctx, id, token, "STORAGE_SCAN_FAILED")
	}
	for _, key := range keys {
		if err = s.store.Remove(key); err != nil {
			return true, s.failDeletion(ctx, id, token, "STORAGE_DELETE_FAILED")
		}
	}
	var jobIDs []string
	jobRows, queryErr := s.pool.Query(ctx, `SELECT j.id::text FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id WHERE r.session_id=$1`, sessionID)
	if queryErr != nil {
		return true, s.failDeletion(ctx, id, token, "WORKER_TEMP_SCAN_FAILED")
	}
	for jobRows.Next() {
		var jobID string
		if queryErr = jobRows.Scan(&jobID); queryErr != nil {
			break
		}
		jobIDs = append(jobIDs, jobID)
	}
	if queryErr == nil {
		queryErr = jobRows.Err()
	}
	jobRows.Close()
	if queryErr != nil {
		return true, s.failDeletion(ctx, id, token, "WORKER_TEMP_SCAN_FAILED")
	}
	if err = s.removeWorkerTemp(jobIDs); err != nil {
		return true, s.failDeletion(ctx, id, token, "WORKER_TEMP_DELETE_FAILED")
	}
	var workerCleanupPending bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM teaching.data_deletion_requests d
		JOIN teaching.lesson_sessions ls ON ls.id=d.session_id
		CROSS JOIN LATERAL unnest(d.required_worker_ids) required(worker_id)
		LEFT JOIN teaching.worker_cleanup_receipts receipt ON receipt.deletion_request_id=d.id
			AND receipt.worker_id=required.worker_id AND receipt.storage_generation=ls.storage_generation
		WHERE d.id=$1 AND receipt.worker_id IS NULL)`, id).Scan(&workerCleanupPending); err != nil {
		return true, s.failDeletion(ctx, id, token, "WORKER_CLEANUP_CHECK_FAILED")
	}
	if workerCleanupPending {
		return true, s.blockDeletion(ctx, id, token, "WORKER_CLEANUP_PENDING")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, errors.Join(err, s.failDeletion(ctx, id, token, "DATABASE_DELETE_FAILED"))
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	databaseFailure := func(cause error) (bool, error) {
		_ = tx.Rollback(ctx)
		return true, errors.Join(cause, s.failDeletion(ctx, id, token, "DATABASE_DELETE_FAILED"))
	}
	var classroomStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, sessionID).Scan(&classroomStatus); err != nil {
		return databaseFailure(err)
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM teaching.data_deletion_requests WHERE id=$1 AND lease_token=$2::uuid FOR UPDATE`, id, token).Scan(&status); err != nil || status != "processing" {
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "processing") {
			return true, err
		}
		return databaseFailure(err)
	}
	if _, err = tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return databaseFailure(err)
	}
	statements := []string{
		`DELETE FROM teaching.live_captures WHERE session_id=$1`,
		`UPDATE teaching.model_calls SET run_id=NULL,job_id=NULL WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1)`,
		`DELETE FROM teaching.export_jobs WHERE session_id=$1`,
		`UPDATE teaching.reports SET parent_report_id=NULL WHERE session_id=$1`, `DELETE FROM teaching.reports WHERE session_id=$1`,
		`DELETE FROM teaching.evidence_items WHERE session_id=$1`, `DELETE FROM teaching.transcript_segments WHERE session_id=$1`,
		`DELETE FROM teaching.job_artifacts WHERE session_id=$1`, `DELETE FROM teaching.analysis_jobs WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1)`,
		`UPDATE teaching.transcript_revisions SET parent_revision_id=NULL WHERE session_id=$1`, `DELETE FROM teaching.transcript_revisions WHERE session_id=$1`,
		`DELETE FROM teaching.analysis_runs WHERE session_id=$1`,
		`UPDATE teaching.media_assets SET playback_asset_id=NULL,parent_asset_id=NULL WHERE session_id=$1`, `DELETE FROM teaching.media_assets WHERE session_id=$1`,
		`DELETE FROM teaching.source_records WHERE session_id=$1`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, sessionID); err != nil {
			return databaseFailure(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.lesson_sessions SET status='deleted',title='[deleted]' WHERE id=$1`, sessionID); err != nil {
		return databaseFailure(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.data_deletion_requests SET status='succeeded',cleanup_manifest=jsonb_build_object('storage_objects_removed',$3::int,'worker_temp_jobs_removed',$4::int,'database_content_removed',true,'managed_backups','not_configured'),completed_at=now(),error_code=NULL,executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2::uuid`, id, token, len(keys), len(jobIDs)); err != nil {
		return databaseFailure(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(action,resource_type,resource_id,result,request_id,metadata) VALUES('classroom.delete.complete','lesson_session',$1,'success','governance',jsonb_build_object('deletion_request_id',$2::text,'storage_objects_removed',$3::int))`, sessionID, id, len(keys)); err != nil {
		return databaseFailure(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return databaseFailure(err)
	}
	return true, nil
}

func (s *Service) removeWorkerTemp(jobIDs []string) error {
	if s.cfg.WorkerTempRoot == "" {
		return nil
	}
	root, err := filepath.Abs(s.cfg.WorkerTempRoot)
	if err != nil {
		return err
	}
	for _, jobID := range jobIDs {
		for _, candidate := range []string{filepath.Join(root, jobID), filepath.Join(root, "jobs", jobID)} {
			rel, relErr := filepath.Rel(root, candidate)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("worker temp path escapes root")
			}
			if err = os.RemoveAll(candidate); err != nil {
				return err
			}
		}
		matches, globErr := filepath.Glob(filepath.Join(root, jobID+"-*"))
		if globErr != nil {
			return globErr
		}
		for _, match := range matches {
			if err = os.RemoveAll(match); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) blockDeletion(ctx context.Context, id, token, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE teaching.data_deletion_requests SET status='blocked',error_code=$3,available_at=now()+$4::interval,executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2::uuid`, id, token, code, s.cfg.Recheck.String())
	return err
}

func (s *Service) failDeletion(ctx context.Context, id, token, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE teaching.data_deletion_requests SET
		status=CASE WHEN attempts>=$3 THEN 'failed' ELSE 'requested' END,error_code=$4,
		available_at=now()+$5::interval,executor_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1 AND lease_token=$2::uuid`, id, token, s.cfg.MaxAttempts, code, s.cfg.Recheck.String())
	return err
}

func (s *Service) RecordWorker(ctx context.Context, id string, capabilities []string) error {
	sort.Strings(capabilities)
	_, err := s.pool.Exec(ctx, `INSERT INTO teaching.governance_workers(worker_id,capabilities,last_seen_at) VALUES($1,$2,now()) ON CONFLICT(worker_id) DO UPDATE SET capabilities=EXCLUDED.capabilities,last_seen_at=now()`, id, capabilities)
	return err
}

func (s *Service) ConfirmWorkerCleanup(ctx context.Context, deletionID, workerID string, generation int64) error {
	if strings.TrimSpace(workerID) == "" || generation <= 0 {
		return invalid("worker_id and storage_generation are required")
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO teaching.worker_cleanup_receipts(deletion_request_id,worker_id,storage_generation)
		SELECT d.id,$2,$3 FROM teaching.data_deletion_requests d JOIN teaching.lesson_sessions ls ON ls.id=d.session_id
		WHERE d.id=$1 AND d.status IN('requested','processing','blocked') AND ls.status='deleting'
		AND ls.storage_generation=$3 AND $2=ANY(d.required_worker_ids)
		ON CONFLICT(deletion_request_id,worker_id) DO UPDATE SET storage_generation=EXCLUDED.storage_generation,confirmed_at=now()`, deletionID, workerID, generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return conflict("INVALID_STATE", "worker cleanup does not match an active deletion generation")
	}
	return nil
}

func (s *Service) PendingWorkerCleanup(ctx context.Context, workerID string) (*WorkerCleanup, error) {
	var value WorkerCleanup
	err := s.pool.QueryRow(ctx, `SELECT d.id::text,d.session_id::text,ls.storage_generation,
		COALESCE((SELECT array_agg(j.id::text ORDER BY j.id) FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id WHERE r.session_id=d.session_id),'{}'::text[])
		FROM teaching.data_deletion_requests d JOIN teaching.lesson_sessions ls ON ls.id=d.session_id
		WHERE d.status IN('requested','processing','blocked') AND $1=ANY(d.required_worker_ids)
		AND NOT EXISTS(SELECT 1 FROM teaching.worker_cleanup_receipts receipt WHERE receipt.deletion_request_id=d.id
			AND receipt.worker_id=$1 AND receipt.storage_generation=ls.storage_generation)
		ORDER BY d.created_at,d.id LIMIT 1`, workerID).Scan(&value.DeletionRequestID, &value.SessionID, &value.StorageGeneration, &value.JobIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Service) SweepExpired(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id::text,m.object_key FROM teaching.media_assets m JOIN teaching.lesson_sessions ls ON ls.id=m.session_id WHERE m.status<>'deleted' AND (m.expires_at<=now() OR ls.content_expires_at<=now()) ORDER BY m.expires_at LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type expiredObject struct{ id, key string }
	var objects []expiredObject
	for rows.Next() {
		var item expiredObject
		if err = rows.Scan(&item.id, &item.key); err != nil {
			rows.Close()
			return 0, err
		}
		objects = append(objects, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, item := range objects {
		if err = s.store.Remove(item.key); err != nil {
			continue
		}
		tx, beginErr := s.pool.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			return removed, beginErr
		}
		_, beginErr = tx.Exec(ctx, `UPDATE teaching.evidence_items SET availability='expired' WHERE availability='available' AND (media_asset_id=$1 OR frame_asset_id=$1)`, item.id)
		if beginErr == nil {
			_, beginErr = tx.Exec(ctx, `UPDATE teaching.export_jobs SET status='expired',updated_at=now() WHERE output_asset_id=$1 AND status='succeeded'`, item.id)
		}
		if beginErr == nil {
			_, beginErr = tx.Exec(ctx, `UPDATE teaching.media_assets SET status='deleted' WHERE id=$1`, item.id)
		}
		if beginErr == nil {
			beginErr = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if beginErr != nil {
			return removed, beginErr
		}
		removed++
	}
	_, err = s.pool.Exec(ctx, `UPDATE teaching.evidence_items e SET availability='expired' FROM teaching.lesson_sessions ls WHERE e.session_id=ls.id AND e.availability='available' AND ls.content_expires_at<=now()`)
	if err == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM teaching.audit_logs WHERE created_at<now()-$1::interval`, s.cfg.AuditRetention.String())
	}
	return removed, err
}

func (s *Service) GetOperations(ctx context.Context, p identity.Principal) (Operations, error) {
	if !p.Has("sys_admin") {
		return Operations{}, forbidden()
	}
	out := Operations{Workers: []Worker{}}
	rows, err := s.pool.Query(ctx, `SELECT worker_id,last_seen_at,capabilities FROM teaching.governance_workers ORDER BY worker_id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var w Worker
		if err = rows.Scan(&w.WorkerID, &w.LastSeenAt, &w.Capabilities); err != nil {
			rows.Close()
			return out, err
		}
		out.Workers = append(out.Workers, w)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM teaching.analysis_jobs WHERE status='queued'`).Scan(&out.QueuedJobs); err != nil {
		return out, err
	}
	local := s.now().In(time.FixedZone("Asia/Shanghai", 8*3600))
	period := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, local.Location()).Format("2006-01-02")
	var charged, reserved, unknown int64
	err = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN status IN('succeeded','failed') THEN round(actual_cost*1000000)::bigint ELSE 0 END),0),COALESCE(sum(CASE WHEN status='reserved' THEN round(reserved_cost*1000000)::bigint ELSE 0 END),0),COALESCE(sum(CASE WHEN status='unknown' THEN round(reserved_cost*1000000)::bigint ELSE 0 END),0) FROM teaching.model_calls WHERE currency=$1 AND billing_period=$2::date`, s.cfg.BudgetCurrency, period).Scan(&charged, &reserved, &unknown)
	if err != nil {
		return out, err
	}
	out.Budget = Budget{s.cfg.BudgetCurrency, period, money(s.cfg.BudgetMonthlyMicros), money(charged), money(reserved), money(unknown)}
	capacity, ok := s.store.(interface{ FreeBytes() (int64, error) })
	if !ok {
		return out, fmt.Errorf("storage backend does not expose capacity")
	}
	out.StorageFreeBytes, err = capacity.FreeBytes()
	if err != nil {
		return out, err
	}
	return out, nil
}
func money(micros int64) string {
	return strconv.FormatInt(micros/1_000_000, 10) + "." + fmt.Sprintf("%06d", micros%1_000_000)
}

func (s *Service) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for {
			worked, err := s.ProcessExport(ctx)
			if err != nil || !worked {
				break
			}
		}
		_, _ = s.SweepExpired(ctx)
		for {
			worked, err := s.ProcessDeletion(ctx)
			if err != nil || !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
