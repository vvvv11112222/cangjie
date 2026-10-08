package analysis

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

const SchemaVersion = "1.1"

var mediaStages = map[string]bool{"probe": true, "audio_analysis": true, "video_analysis": true}

type Config struct {
	Lease, ProbeTimeout, ASRTimeout, VideoTimeout    time.Duration
	MediaJobAttempts                                 int
	ProcessorVersion, FFmpegSHA256                   string
	ASRModelName, ASRModelRevision, ASRDevice        string
	KeyframeIntervalMS, MaxKeyframes, MaxVideoHeight int
	MaxMediaDurationMS, MaxArtifactBytes             int64
	GoStageTimeout                                   time.Duration
	ReportEnabled                                    bool
	ReportAPIBase, ReportAPIKey                      string
	ReportModel, ReportModelRevision                 string
	ReportPromptVersion, ReportPromptSHA256          string
	ReportSelectionVersion                           string
	ReportPriceVersion                               string
	ReportTimeout                                    time.Duration
	ReportMaxInputTokens, ReportMaxOutputTokens      int
}

type Service struct {
	pool  *pgxpool.Pool
	store storage.Backend
	cfg   Config
}

type CreateRun struct {
	MediaAssetID            string  `json:"media_asset_id"`
	Mode                    string  `json:"mode"`
	InputTranscriptRevision *string `json:"input_transcript_revision_id"`
	ConfigProfile           string  `json:"config_profile"`
}

type Job struct {
	ID                  string     `json:"id"`
	Stage               string     `json:"stage"`
	Status              string     `json:"status"`
	Progress            int        `json:"progress"`
	ErrorCode           *string    `json:"error_code"`
	Attempts            int        `json:"attempts"`
	MaxAttempts         int        `json:"max_attempts"`
	ExecutionDeadlineAt *time.Time `json:"execution_deadline_at"`
}

type Run struct {
	ID                        string   `json:"id"`
	SessionID                 string   `json:"session_id"`
	MediaAssetID              string   `json:"media_asset_id"`
	Mode                      string   `json:"mode"`
	InputTranscriptRevisionID *string  `json:"input_transcript_revision_id"`
	Status                    string   `json:"status"`
	Jobs                      []Job    `json:"jobs"`
	StagePlan                 []string `json:"stage_plan"`
	ReportID                  *string  `json:"report_id"`
	ErrorCode                 *string  `json:"error_code"`
	Limitations               []string `json:"limitations"`
	AllowedActions            []string `json:"allowed_actions"`
}

type ClaimRequest struct {
	WorkerID     string   `json:"worker_id"`
	Capabilities []string `json:"capabilities"`
}

type Model struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

type Execution struct {
	ProcessorVersion  string `json:"processor_version"`
	FFmpegBuildSHA256 string `json:"ffmpeg_build_sha256"`
	ASRModel          *Model `json:"asr_model"`
	ParametersSHA256  string `json:"parameters_sha256"`
}

type Parameters struct {
	ConfigProfile      string `json:"config_profile"`
	KeyframeIntervalMS int    `json:"keyframe_interval_ms"`
	MaxKeyframes       int    `json:"max_keyframes"`
	ASRDevice          string `json:"asr_device"`
}

type Claim struct {
	SchemaVersion       string     `json:"schema_version"`
	JobID               string     `json:"job_id"`
	RunID               string     `json:"run_id"`
	SessionID           string     `json:"session_id"`
	MediaAssetID        string     `json:"media_asset_id"`
	Stage               string     `json:"stage"`
	LeaseToken          string     `json:"lease_token"`
	LeaseExpiresAt      time.Time  `json:"lease_expires_at"`
	InputURL            string     `json:"input_url"`
	InputSHA256         string     `json:"input_sha256"`
	DurationMS          *int64     `json:"duration_ms"`
	Parameters          Parameters `json:"parameters"`
	Execution           Execution  `json:"execution"`
	ExecutionDeadlineAt time.Time  `json:"execution_deadline_at"`
	RightsVersion       int        `json:"rights_version"`
	StorageGeneration   int64      `json:"storage_generation"`
}

type Heartbeat struct {
	Progress int `json:"progress"`
}
type HeartbeatResult struct {
	LeaseExpiresAt  time.Time `json:"lease_expires_at"`
	CancelRequested bool      `json:"cancel_requested"`
}

type ArtifactInput struct {
	ArtifactKey    string
	Kind           string
	TimestampMS    *int64
	OriginOffsetMS int64
}
type Artifact struct {
	AssetID  string `json:"asset_id"`
	SHA256   string `json:"sha256"`
	ByteSize int64  `json:"byte_size"`
}

type CompleteResult struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate"`
}
type Fail struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type FailResult struct {
	JobID       string     `json:"job_id"`
	Status      string     `json:"status"`
	NextRetryAt *time.Time `json:"next_retry_at"`
}

type InputContent struct {
	File     storage.ReadSeekCloser
	Size     int64
	MIMEType string
}

type Interval struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}
type Segment struct {
	SegmentNo int    `json:"segment_no"`
	StartMS   int64  `json:"start_ms"`
	EndMS     int64  `json:"end_ms"`
	Text      string `json:"text_content"`
	Speaker   string `json:"speaker_label"`
}
type Frame struct {
	AssetID     string `json:"asset_id"`
	TimestampMS int64  `json:"timestamp_ms"`
}

type resultEnvelope struct {
	SchemaVersion string    `json:"schema_version"`
	JobID         string    `json:"job_id"`
	RunID         string    `json:"run_id"`
	SessionID     string    `json:"session_id"`
	MediaAssetID  string    `json:"media_asset_id"`
	Stage         string    `json:"stage"`
	Execution     Execution `json:"execution"`
}
type ProbeResult struct {
	resultEnvelope
	DurationMS      int64    `json:"duration_ms"`
	HasAudio        bool     `json:"has_audio"`
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	VideoCodec      string   `json:"video_codec"`
	PlaybackAssetID string   `json:"playback_asset_id"`
	OriginOffsetMS  int64    `json:"origin_offset_ms"`
	Limitations     []string `json:"limitations"`
}
type AudioResult struct {
	resultEnvelope
	Segments     []Segment  `json:"segments"`
	AudioAssetID *string    `json:"audio_asset_id"`
	Model        Model      `json:"model"`
	Coverage     []Interval `json:"coverage"`
	Limitations  []string   `json:"limitations"`
}
type VideoResult struct {
	resultEnvelope
	Frames   []Frame           `json:"frames"`
	Events   []json.RawMessage `json:"events"`
	Model    any               `json:"model"`
	Sampling struct {
		IntervalMS int `json:"interval_ms"`
		MaxFrames  int `json:"max_frames"`
	} `json:"sampling"`
	Coverage    []Interval `json:"coverage"`
	Limitations []string   `json:"limitations"`
}

type Results struct {
	TranscriptRevisionID *string           `json:"transcript_revision_id"`
	Segments             []Segment         `json:"segments"`
	Evidence             []json.RawMessage `json:"evidence"`
	Frames               []Frame           `json:"frames"`
	Events               []json.RawMessage `json:"events"`
	Limitations          []string          `json:"limitations"`
	RunStatus            string            `json:"run_status"`
}

type Evidence struct {
	ID                  string         `json:"id"`
	RunID               string         `json:"run_id"`
	SessionID           string         `json:"session_id"`
	MediaAssetID        string         `json:"media_asset_id"`
	Kind                string         `json:"kind"`
	StartMS             int64          `json:"start_ms"`
	EndMS               int64          `json:"end_ms"`
	TranscriptSegmentID *string        `json:"transcript_segment_id"`
	FrameAssetID        *string        `json:"frame_asset_id"`
	Availability        string         `json:"availability"`
	Description         string         `json:"description"`
	Provenance          map[string]any `json:"provenance"`
}

type Dimension struct {
	DimensionCode      string     `json:"dimension_code"`
	CoverageStatus     string     `json:"coverage_status"`
	Summary            string     `json:"summary"`
	Limitation         string     `json:"limitation"`
	Coverage           []Interval `json:"coverage"`
	SummaryEvidenceIDs []string   `json:"summary_evidence_ids"`
}

type CandidateObservation struct {
	DimensionCode   string   `json:"dimension_code"`
	ObservationType string   `json:"observation_type"`
	ObservationText string   `json:"observation_text"`
	Suggestion      string   `json:"suggestion"`
	EvidenceIDs     []string `json:"evidence_ids"`
}

type ReportCandidate struct {
	Summary            string                 `json:"summary"`
	SummaryEvidenceIDs []string               `json:"summary_evidence_ids"`
	Dimensions         []Dimension            `json:"dimensions"`
	Observations       []CandidateObservation `json:"observations"`
}

type Report struct {
	ID                    string         `json:"id"`
	RunID                 string         `json:"run_id"`
	SessionID             string         `json:"session_id"`
	Revision              int            `json:"revision"`
	LockVersion           int            `json:"lock_version"`
	Status                string         `json:"status"`
	Summary               string         `json:"summary"`
	Dimensions            []Dimension    `json:"dimensions"`
	Observations          []Observation  `json:"observations"`
	SummaryEvidenceIDs    []string       `json:"summary_evidence_ids"`
	ContentSHA256         *string        `json:"content_sha256"`
	ReviewedContentSHA256 *string        `json:"reviewed_content_sha256"`
	ReviewedBy            *string        `json:"reviewed_by"`
	ReviewedAt            *time.Time     `json:"reviewed_at"`
	Provenance            map[string]any `json:"provenance"`
	AllowedActions        []string       `json:"allowed_actions"`
}

type Observation struct {
	ID              string   `json:"id"`
	DimensionCode   string   `json:"dimension_code"`
	ObservationType string   `json:"observation_type"`
	ObservationText string   `json:"observation_text"`
	Suggestion      string   `json:"suggestion"`
	ReviewStatus    string   `json:"review_status"`
	EvidenceIDs     []string `json:"evidence_ids"`
}

type Revision struct {
	ID                    string    `json:"id"`
	SessionID             string    `json:"session_id"`
	MediaAssetID          string    `json:"media_asset_id"`
	SourceRunID           string    `json:"source_run_id"`
	ParentRevisionID      *string   `json:"parent_revision_id"`
	RevisionNo            int       `json:"revision_no"`
	SourceType            string    `json:"source_type"`
	ContentSHA256         string    `json:"content_sha256"`
	CreatedBy             string    `json:"created_by"`
	CreatedAt             time.Time `json:"created_at"`
	Reason                string    `json:"reason"`
	TranscriptLockVersion int       `json:"transcript_lock_version"`
	Segments              []Segment `json:"segments"`
}
