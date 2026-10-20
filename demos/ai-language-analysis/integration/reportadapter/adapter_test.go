package reportadapter

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) (sourceReport, modelInput, Context) {
	t.Helper()
	raw, err := os.ReadFile("../testdata/adapter-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Report  sourceReport `json:"report"`
		Input   modelInput   `json:"model_input"`
		Context Context      `json:"context"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f.Input.Evidence[0].SHA256 = SHA256([]byte(f.Input.Evidence[0].Text))
	return f.Report, f.Input, f.Context
}

func convertFixture(t *testing.T, r sourceReport, i modelInput, c Context) (Result, error) {
	t.Helper()
	raw, _ := json.Marshal(r)
	input, _ := json.Marshal(i)
	c.ManifestSHA256 = SHA256(input)
	return Convert(raw, input, c)
}

func TestPreservesCandidateAdviceCoverageAndOriginalExtensions(t *testing.T) {
	r, i, c := fixture(t)
	out, err := convertFixture(t, r, i, c)
	if err != nil {
		t.Fatal(err)
	}
	if out.Candidate.IDs[0] != i.Evidence[0].ID || len(out.Candidate.Dimensions[0].Coverage) != 2 || out.Candidate.Dimensions[0].Coverage[1].StartMS != 20000 {
		t.Fatal("mapping or coverage lost")
	}
	if out.Candidate.Observations[0].Suggestion != r.Observations[0].Suggestion {
		t.Fatal("advice lost")
	}
	var preserved sourceReport
	if err := json.Unmarshal(out.Extensions.SourceReport, &preserved); err != nil {
		t.Fatal(err)
	}
	var originalAudit, preservedAudit any
	_ = json.Unmarshal(r.Audit, &originalAudit)
	_ = json.Unmarshal(preserved.Audit, &preservedAudit)
	if preserved.Observations[0].Analysis != r.Observations[0].Analysis || preserved.Observations[0].Verification != r.Observations[0].Verification || !reflect.DeepEqual(originalAudit, preservedAudit) {
		t.Fatal("coaching or audit discarded")
	}
	raw, _ := json.Marshal(out.Candidate)
	if out.Extensions.CandidateSHA256 != SHA256(raw) || strings.Contains(string(raw), "event_ids") || strings.Contains(string(raw), "verification") {
		t.Fatal("strict candidate polluted or not bound")
	}
}

func TestRejectsUnsafeEvidenceAndUnsupportedClaims(t *testing.T) {
	cases := map[string]func(*sourceReport, *modelInput, *Context){
		"cross run":           func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].RunID = c.SessionID },
		"cross session":       func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].SessionID = c.RunID },
		"cross media":         func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].MediaAssetID = c.RunID },
		"expired":             func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].Availability = "expired" },
		"unselected same run": func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].EvidenceID = c.RunID },
		"changed text":        func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].Text += "另一个版本" },
		"changed timing":      func(r *sourceReport, i *modelInput, c *Context) { c.Bindings[0].EndMS++ },
		"input excerpt truncation": func(r *sourceReport, i *modelInput, c *Context) {
			i.Evidence[0].Text = "先比较"
			i.Evidence[0].SHA256 = SHA256([]byte(i.Evidence[0].Text))
		},
		"bad text hash":        func(r *sourceReport, i *modelInput, c *Context) { i.Evidence[0].SHA256 = strings.Repeat("0", 64) },
		"duplicate selected":   func(r *sourceReport, i *modelInput, c *Context) { i.Evidence = append(i.Evidence, i.Evidence[0]) },
		"duplicate binding":    func(r *sourceReport, i *modelInput, c *Context) { c.Bindings = append(c.Bindings, c.Bindings[0]) },
		"unconfirmed timeline": func(r *sourceReport, i *modelInput, c *Context) { c.SourceTimelineConfirmed = false },
		"outside duration":     func(r *sourceReport, i *modelInput, c *Context) { c.DurationMS = 25000 },
		"coverage gap": func(r *sourceReport, i *modelInput, c *Context) {
			i.Evidence[0].EndMS = 25000
			c.Bindings[0].EndMS = 25000
		},
		"overlapping coverage": func(r *sourceReport, i *modelInput, c *Context) { i.Coverage[1].StartMS = 9000 },
		"missing coverage":     func(r *sourceReport, i *modelInput, c *Context) { i.Coverage = nil },
		"wrong revision":       func(r *sourceReport, i *modelInput, c *Context) { c.TranscriptRevisionID = c.RunID },
		"wrong source hash":    func(r *sourceReport, i *modelInput, c *Context) { c.InputSHA256 = strings.Repeat("0", 64) },
		"invalid UUID":         func(r *sourceReport, i *modelInput, c *Context) { c.RunID = "job-001" },
		"unknown citation":     func(r *sourceReport, i *modelInput, c *Context) { r.Observations[0].IDs = []string{"seg-999"} },
		"duplicate citation": func(r *sourceReport, i *modelInput, c *Context) {
			r.Observations[0].IDs = []string{"seg-001", "seg-001"}
		},
		"limited dimension claims": func(r *sourceReport, i *modelInput, c *Context) { r.Dimensions[1].Summary = "学生理解充分" },
		"duplicate dimension":      func(r *sourceReport, i *modelInput, c *Context) { r.Dimensions[1].Code = "content" },
		"null observations":        func(r *sourceReport, i *modelInput, c *Context) { r.Observations = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, i, c := fixture(t)
			mutate(&r, &i, &c)
			if _, err := convertFixture(t, r, i, c); err == nil {
				t.Fatal("invalid integration input accepted")
			}
		})
	}
}

func TestPreservesEmptyObservationsWithoutInventingFindings(t *testing.T) {
	for _, allInsufficient := range []bool{false, true} {
		r, i, c := fixture(t)
		r.Observations = []sourceObservation{}
		if allInsufficient {
			r.Summary, r.IDs = "", []string{}
			for n := range r.Dimensions {
				r.Dimensions[n].Status = "insufficient"
				r.Dimensions[n].Summary, r.Dimensions[n].IDs = "", []string{}
				r.Dimensions[n].Limitation = "没有足够教学事件。"
			}
		}
		out, err := convertFixture(t, r, i, c)
		if err != nil {
			t.Fatal("valid empty observation list rejected", err)
		}
		if out.Candidate.Observations == nil || len(out.Candidate.Observations) != 0 || len(out.Candidate.Dimensions) != 6 {
			t.Fatal("empty findings must remain a non-null empty array")
		}
		var preserved sourceReport
		if err := json.Unmarshal(out.Extensions.SourceReport, &preserved); err != nil || preserved.Observations == nil || len(preserved.Observations) != 0 {
			t.Fatal("original empty report changed", err)
		}
	}
}

func TestManifestByteIntegrityAndUnknownSourceFields(t *testing.T) {
	r, i, c := fixture(t)
	raw, _ := json.Marshal(r)
	input, _ := json.Marshal(i)
	c.ManifestSHA256 = SHA256(input)
	if _, err := Convert(raw, append(input, ' '), c); err == nil {
		t.Fatal("changed manifest accepted")
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["published"] = true
	unknown, _ := json.Marshal(fields)
	if _, err := Convert(unknown, input, c); err == nil {
		t.Fatal("unknown source field silently discarded")
	}
}

func TestExtensionDigestSurvivesJSONPresentation(t *testing.T) {
	r, i, c := fixture(t)
	r.Observations[0].Analysis = "讨论 A < B & C > D 的条件。"
	out, err := convertFixture(t, r, i, c)
	if err != nil {
		t.Fatal(err)
	}
	pretty, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(pretty, &decoded); err != nil {
		t.Fatal(err)
	}
	stableSource, _ := json.Marshal(decoded.Extensions.SourceReport)
	stableCandidate, _ := json.Marshal(decoded.Candidate)
	if SHA256(stableSource) != decoded.Extensions.SourceReportSHA256 || SHA256(stableCandidate) != decoded.Extensions.CandidateSHA256 {
		t.Fatal("presentation changed extension integrity")
	}
}
