package analysis

import (
	_ "embed"
	"fmt"
)

const TeacherPromptVersion = "teacher-sixdim-v1"
const ComprehensivePromptVersion = "teacher-sixdim-v2"

//go:embed prompts/teacher-sixdim-v1.txt
var teacherReportPrompt string

//go:embed prompts/teacher-sixdim-v2.txt
var comprehensiveReportPrompt string

// ReportPrompt returns the exact bytes sent as the system message and their hash.
// The embedded template makes execution independent of the server's working directory.
func ReportPrompt(version string) (text, sha256 string, err error) {
	switch version {
	case "p0-v1":
		text = reportSystemPrompt
	case TeacherPromptVersion:
		text = teacherReportPrompt
	case ComprehensivePromptVersion:
		text = comprehensiveReportPrompt
	default:
		return "", "", fmt.Errorf("unsupported REPORT_PROMPT_VERSION %q", version)
	}
	return text, digestBytes([]byte(text)), nil
}

// ValidateReportPrompt verifies new template opt-ins before any paid dispatch.
// p0-v1 retains its legacy configuration semantics; persisted old snapshots are
// always executed using their own SystemPrompt, never the current template.
func ValidateReportPrompt(enabled bool, version, sha256 string) error {
	if !enabled {
		return nil
	}
	_, expected, err := ReportPrompt(version)
	if err != nil {
		return err
	}
	if (version == TeacherPromptVersion || version == ComprehensivePromptVersion) && sha256 != expected {
		return fmt.Errorf("REPORT_PROMPT_SHA256 does not match %s; run go run ./cmd/report-prompt", version)
	}
	return nil
}
