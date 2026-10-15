package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
)

type Config struct {
	PublicURL, MediaURL, ControlURL, ControlToken, EventToken, RecordRoot, FFmpeg, FFprobe string
	PairingTTL, ViewerTTL, MaxDuration, WaitingTimeout, DisconnectTimeout, FinalizeTimeout time.Duration
	MaxBytes                                                                               int64
}
type Service struct {
	pool     *pgxpool.Pool
	media    *media.Service
	analysis *analysis.Service
	identity *identity.Service
	cfg      Config
	control  *Client
}

func (s *Service) EventToken() string { return s.cfg.EventToken }

type Capture struct {
	ID              string     `json:"id"`
	SessionID       string     `json:"session_id"`
	State           string     `json:"state"`
	ConnectionState string     `json:"connection_state"`
	RecordingState  string     `json:"recording_state"`
	AudioTrack      string     `json:"audio_track"`
	VideoTrack      string     `json:"video_track"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	StartedAt       *time.Time `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationMS      *int64     `json:"duration_ms"`
	MediaID         *string    `json:"media_asset_id"`
	RunID           *string    `json:"media_prepare_run_id"`
	EndReason       *string    `json:"end_reason"`
	ErrorCode       *string    `json:"error_code"`
	AllowedActions  []string   `json:"allowed_actions"`
}
type record struct {
	Capture
	SourceID, UserID, Path string
	Generation             int64
	Rights                 int
	StopAt, DisconnectAt   *time.Time
}
type Create struct {
	SourceID string `json:"source_record_id"`
	Profile  string `json:"config_profile"`
}
type Pairing struct {
	ID        string    `json:"pairing_id"`
	URL       string    `json:"phone_url"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Exchange struct {
	Token string `json:"pairing_token"`
}
type Publisher struct {
	CaptureID    string    `json:"live_capture_id"`
	SessionID    string    `json:"session_id"`
	WHIPURL      string    `json:"whip_url"`
	PublishToken string    `json:"publish_token"`
	ControlToken string    `json:"control_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type Viewer struct {
	ID        string    `json:"viewer_session_id"`
	URL       string    `json:"whep_url"`
	Token     string    `json:"view_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthRequest is the native MediaMTX v1.15.3 HTTP authentication payload.
type AuthRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Token    string `json:"token"`
	IP       string `json:"ip"`
	Action   string `json:"action"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	ID       string `json:"id"`
	Query    string `json:"query"`
}
type Event struct {
	ID           string    `json:"event_id"`
	Type         string    `json:"type"`
	Path         string    `json:"path"`
	ConnectionID string    `json:"connection_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	SegmentPath  *string   `json:"segment_path"`
}

func digest(v any) string        { b, _ := json.Marshal(v); return hash(string(b)) }
func hash(v string) string       { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func bad(code, msg string) error { return apperror.New(http.StatusConflict, code, msg) }
func invalid(msg string) error   { return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", msg) }
func denied() error {
	return apperror.New(http.StatusForbidden, "FORBIDDEN", "capture access is not permitted")
}
func missing() error { return apperror.New(http.StatusNotFound, "NOT_FOUND", "capture not found") }
func unavailable() error {
	return apperror.New(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "media service is unavailable")
}
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}
