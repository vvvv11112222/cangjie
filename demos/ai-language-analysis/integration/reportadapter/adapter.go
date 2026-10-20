// Package reportadapter converts a demo report to the pinned main-project
// ReportCandidate. It performs no network calls, persistence or publication.
package reportadapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const UpstreamCommit = "8dc69286073090fffc5120a5d30946bfe883ee85"

type Interval struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

type Dimension struct {
	Code       string     `json:"dimension_code"`
	Status     string     `json:"coverage_status"`
	Summary    string     `json:"summary"`
	Limitation string     `json:"limitation"`
	Coverage   []Interval `json:"coverage"`
	IDs        []string   `json:"summary_evidence_ids"`
}

type Observation struct {
	Code       string   `json:"dimension_code"`
	Type       string   `json:"observation_type"`
	Text       string   `json:"observation_text"`
	Suggestion string   `json:"suggestion"`
	IDs        []string `json:"evidence_ids"`
}

type Candidate struct {
	Summary      string        `json:"summary"`
	IDs          []string      `json:"summary_evidence_ids"`
	Dimensions   []Dimension   `json:"dimensions"`
	Observations []Observation `json:"observations"`
}

// Binding must come from the server's evidence registration, not model output.
// Text and interval are the exact demo segment used for report generation.
type Binding struct {
	SegmentID    string `json:"segment_id"`
	EvidenceID   string `json:"evidence_id"`
	RunID        string `json:"run_id"`
	SessionID    string `json:"session_id"`
	MediaAssetID string `json:"media_asset_id"`
	Availability string `json:"availability"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	Text         string `json:"text"`
}

// Context is trusted integration input. Hashes are integrity checks, not proof
// of authorization or of a real provider call. The main service must enforce both.
type Context struct {
	RunID                   string    `json:"run_id"`
	SessionID               string    `json:"session_id"`
	MediaAssetID            string    `json:"media_asset_id"`
	InputSHA256             string    `json:"input_sha256"`
	ManifestSHA256          string    `json:"manifest_sha256"`
	TranscriptRevisionID    string    `json:"transcript_revision_id"`
	DurationMS              int64     `json:"duration_ms"`
	SourceTimelineConfirmed bool      `json:"source_timeline_confirmed"`
	Bindings                []Binding `json:"bindings"`
}

type modelEvidence struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text_content"`
	SHA256  string `json:"text_sha256"`
}

// Only fields used for conversion are read; the original full ModelInput bytes
// remain bound by ManifestSHA256, including upstream model/prompt metadata.
type modelInput struct {
	SchemaVersion        string          `json:"schema_version"`
	RunID                string          `json:"run_id"`
	SessionID            string          `json:"session_id"`
	MediaAssetID         string          `json:"media_asset_id"`
	InputSHA256          string          `json:"input_sha256"`
	TranscriptRevisionID *string         `json:"transcript_revision_id"`
	Coverage             []Interval      `json:"coverage"`
	Evidence             []modelEvidence `json:"evidence"`
}

type sourceObservation struct {
	Observation
	Title          string   `json:"title"`
	Analysis       string   `json:"analysis"`
	Verification   string   `json:"verification"`
	EventIDs       []string `json:"event_ids"`
	AdviceTemplate string   `json:"advice_template"`
}
type sourceDimension struct {
	Code       string   `json:"dimension_code"`
	Status     string   `json:"coverage_status"`
	Summary    string   `json:"summary"`
	Limitation string   `json:"limitation"`
	IDs        []string `json:"summary_evidence_ids"`
}
type sourceReport struct {
	Summary         string              `json:"summary"`
	IDs             []string            `json:"summary_evidence_ids"`
	Dimensions      []sourceDimension   `json:"dimensions"`
	Observations    []sourceObservation `json:"observations"`
	AnalysisVersion string              `json:"analysis_version"`
	Audit           json.RawMessage     `json:"quality_audit"`
}

// Extensions is a separate local artifact. Never send it to a strict
// ReportCandidate endpoint. Keeping the whole source preserves coaching content
// and unresolved audit findings without inventing upstream protocol fields.
type Extensions struct {
	UpstreamCommit     string          `json:"upstream_commit"`
	RunID              string          `json:"run_id"`
	ManifestSHA256     string          `json:"manifest_sha256"`
	CandidateSHA256    string          `json:"candidate_sha256"`
	SourceReportSHA256 string          `json:"source_report_sha256"`
	SourceReport       json.RawMessage `json:"source_report"`
}
type Result struct {
	Candidate  Candidate  `json:"candidate"`
	Extensions Extensions `json:"extensions"`
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func SHA256(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func decode(raw []byte, value any, strict bool) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

func validIntervals(windows []Interval, duration int64) bool {
	var previous int64
	for _, w := range windows {
		if w.StartMS < previous || w.EndMS <= w.StartMS || w.EndMS > duration {
			return false
		}
		previous = w.EndMS
	}
	return true
}
func covered(start, end int64, windows []Interval) bool {
	for _, w := range windows {
		if start >= w.StartMS && end <= w.EndMS {
			return true
		}
	}
	return false
}

// Convert requires the exact final selected ModelInput snapshot. It rejects a
// partially truncated segment instead of assigning its full-text conclusions to
// an excerpt the model may not have seen. Regenerate against that excerpt first.
func Convert(reportJSON, modelInputJSON []byte, ctx Context) (Result, error) {
	fail := func(message string) (Result, error) { return Result{}, fmt.Errorf("report adapter: %s", message) }
	if !uuid.MatchString(ctx.RunID) || !uuid.MatchString(ctx.SessionID) || !uuid.MatchString(ctx.MediaAssetID) || !uuid.MatchString(ctx.TranscriptRevisionID) {
		return fail("server run/session/media/revision UUIDs required")
	}
	if !digest.MatchString(ctx.InputSHA256) || !digest.MatchString(ctx.ManifestSHA256) || SHA256(modelInputJSON) != ctx.ManifestSHA256 {
		return fail("model input manifest digest mismatch")
	}
	if ctx.DurationMS <= 0 || !ctx.SourceTimelineConfirmed {
		return fail("original media timeline must be confirmed")
	}
	var input modelInput
	if err := decode(modelInputJSON, &input, false); err != nil {
		return fail("invalid model input JSON")
	}
	if input.SchemaVersion != "1.1" || input.RunID != ctx.RunID || input.SessionID != ctx.SessionID || input.MediaAssetID != ctx.MediaAssetID || input.InputSHA256 != ctx.InputSHA256 || input.TranscriptRevisionID == nil || *input.TranscriptRevisionID != ctx.TranscriptRevisionID {
		return fail("model input identity or revision mismatch")
	}
	if len(input.Coverage) == 0 || !validIntervals(input.Coverage, ctx.DurationMS) {
		return fail("invalid model input coverage")
	}
	selected := map[string]modelEvidence{}
	for _, e := range input.Evidence {
		if !uuid.MatchString(e.ID) || e.Kind != "transcript" || e.StartMS < 0 || e.StartMS >= e.EndMS || !covered(e.StartMS, e.EndMS, input.Coverage) || strings.TrimSpace(e.Text) == "" || SHA256([]byte(e.Text)) != e.SHA256 {
			return fail("selected evidence has invalid kind, text hash or timeline")
		}
		if _, exists := selected[e.ID]; exists {
			return fail("duplicate selected evidence")
		}
		selected[e.ID] = e
	}
	if len(selected) == 0 {
		return fail("no selected transcript evidence")
	}
	bindings := map[string]Binding{}
	boundEvidence := map[string]bool{}
	for _, b := range ctx.Bindings {
		if b.SegmentID == "" || bindings[b.SegmentID].SegmentID != "" || boundEvidence[b.EvidenceID] {
			return fail("ambiguous evidence binding")
		}
		if b.RunID != ctx.RunID || b.SessionID != ctx.SessionID || b.MediaAssetID != ctx.MediaAssetID || b.Availability != "available" {
			return fail("evidence unavailable or from another run/session/media")
		}
		e, ok := selected[b.EvidenceID]
		if !ok {
			return fail("binding references evidence outside actual model input")
		}
		if b.StartMS != e.StartMS || b.EndMS != e.EndMS || b.Text != e.Text {
			return fail("binding text or timeline differs from actual model input")
		}
		bindings[b.SegmentID], boundEvidence[b.EvidenceID] = b, true
	}
	refs := func(ids []string, required bool) ([]string, error) {
		if ids == nil || (required && len(ids) == 0) {
			return nil, fmt.Errorf("missing evidence array")
		}
		out, seen := make([]string, 0, len(ids)), map[string]bool{}
		for _, id := range ids {
			b, ok := bindings[id]
			if !ok || seen[id] {
				return nil, fmt.Errorf("unknown or duplicate source citation")
			}
			seen[id] = true
			out = append(out, b.EvidenceID)
		}
		return out, nil
	}
	var source sourceReport
	if err := decode(reportJSON, &source, true); err != nil {
		return fail("invalid demo report JSON or unsupported fields")
	}
	if len(source.Dimensions) != 6 || len(source.Observations) == 0 {
		return fail("six dimensions and at least one observation required")
	}
	candidate := Candidate{Summary: source.Summary, Dimensions: []Dimension{}, Observations: []Observation{}}
	var err error
	if (strings.TrimSpace(source.Summary) != "") != (len(source.IDs) > 0) {
		return fail("summary and citations disagree")
	}
	if candidate.IDs, err = refs(source.IDs, false); err != nil {
		return fail(err.Error())
	}
	codes := map[string]bool{"content": false, "pace": false, "thinking": false, "expression": false, "management": false, "technology": false}
	for _, d := range source.Dimensions {
		seen, known := codes[d.Code]
		if !known || seen {
			return fail("invalid or duplicate dimension")
		}
		codes[d.Code] = true
		if d.Status != "observed" && d.Status != "insufficient" && d.Status != "not_applicable" {
			return fail("invalid dimension status")
		}
		if d.Status != "observed" && (strings.TrimSpace(d.Limitation) == "" || strings.TrimSpace(d.Summary) != "" || len(d.IDs) > 0) {
			return fail("limited dimension cannot contain factual claims")
		}
		if (strings.TrimSpace(d.Summary) != "") != (len(d.IDs) > 0) || (d.Status == "observed" && len(d.IDs) == 0) {
			return fail("dimension summary and citations disagree")
		}
		ids, err := refs(d.IDs, d.Status == "observed")
		if err != nil {
			return fail(err.Error())
		}
		// Input coverage describes processed windows, not continuous behavior.
		candidate.Dimensions = append(candidate.Dimensions, Dimension{d.Code, d.Status, d.Summary, d.Limitation, append([]Interval{}, input.Coverage...), ids})
	}
	for _, o := range source.Observations {
		if !codes[o.Code] || strings.TrimSpace(o.Text) == "" || (o.Type != "highlight" && o.Type != "issue" && o.Type != "observation") {
			return fail("invalid observation")
		}
		ids, err := refs(o.IDs, true)
		if err != nil {
			return fail(err.Error())
		}
		candidate.Observations = append(candidate.Observations, Observation{o.Code, o.Type, o.Text, o.Suggestion, ids})
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return fail("candidate encoding failed")
	}
	// Use the same compact/HTML escaping rules as the result encoder so the
	// digest survives display indentation, including text such as "A < B".
	compactSource, err := json.Marshal(json.RawMessage(reportJSON))
	if err != nil {
		return fail("source encoding failed")
	}
	return Result{candidate, Extensions{UpstreamCommit, ctx.RunID, ctx.ManifestSHA256, SHA256(raw), SHA256(compactSource), compactSource}}, nil
}
