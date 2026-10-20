package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func readingFixture() []Segment {
	return []Segment{{ID: "e-0001", Start: 152000, End: 182000, Text: "A matrix is just a."}, {ID: "e-0002", Start: 182000, End: 212000, Text: "Rectangular array of numbers."}}
}

const joinedFixture = `{"boundaries":[{"left_id":"e-0001","right_id":"e-0002","join_sentence":true,"lowercase_next":true,"paragraph_break":false}]}`

func TestReadingJoinAndSources(t *testing.T) {
	segments := readingFixture()
	decisions, err := parseReadingDecisions(joinedFixture, segments)
	if err != nil {
		t.Fatal(err)
	}
	result := buildReading(segments, decisions, "ready", "", ModelMeta{})
	if len(result.Paragraphs) != 1 || result.Paragraphs[0].Text != "A matrix is just a rectangular array of numbers." || strings.Join(result.Paragraphs[0].IDs, ",") != "e-0001,e-0002" {
		t.Fatalf("unexpected: %+v", result)
	}
	if segments[0].Text != "A matrix is just a." || segments[0].End != 182000 {
		t.Fatal("source changed")
	}
	if !sameReadingWords(segments[0].Text, result.Edits[0].Text) || !sameReadingWords(segments[1].Text, result.Edits[1].Text) {
		t.Fatal("word changes")
	}
}
func TestReadingRejectsInvalidDecisions(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(joinedFixture, "e-0002", "unknown", 1),
		strings.Replace(joinedFixture, "e-0001", "e-0002", 1),
		strings.Replace(joinedFixture, `"paragraph_break":false`, `"paragraph_break":true`, 1),
		strings.Replace(joinedFixture, `"join_sentence":true`, `"join_sentence":false`, 1),
		strings.Replace(joinedFixture, `"join_sentence":true`, `"join_sentence":"yes"`, 1),
		strings.Replace(joinedFixture, `"lowercase_next":true`, `"lowercase_next":true,"text":"fake"`, 1),
		joinedFixture + `{}`,
		`{"boundaries":[]}`,
		`{"boundaries":null}`,
	} {
		if _, err := parseReadingDecisions(raw, readingFixture()); err == nil {
			t.Fatalf("accepted invalid boundary: %s", raw)
		}
	}
}
func TestReadingWords(t *testing.T) {
	for _, tc := range []struct {
		source, edited string
		ok             bool
	}{
		{"which has got a lot of.", "which has got a lot of", true},
		{"Exercises from the past.", "exercises from the past.", true},
		{"这是。一个矩阵。", "这是一个矩阵。", true},
		{"拼音 n。和 l。", "拼音 n 和 l。", true},
		{"拼音了和了。", "拼音 n 和 l。", false},
		{"matrix A.", "matrix a.", false},
		{"API is useful.", "api is useful.", false},
		{"n 和 l", "n 和 n", false},
		{"two x minus y", "two x plus y", false},
		{"18.06", "1806", false},
		{".5", "5", false},
		{"1,000", "1 000", false},
		{"2:3", "2 3", false},
		{"web.mit.edu", "web mit edu", false},
		{"na nǎ là", "na na la", false},
		{"don't stop", "do not stop", false},
		{"a rectangular", "arectangular", false},
		{"x + y = 3", "x y = 3", false},
		{"that that is", "that is", false},
	} {
		if got := sameReadingWords(tc.source, tc.edited); got != tc.ok {
			t.Errorf("%q => %q: got %v", tc.source, tc.edited, got)
		}
	}
}
func TestConservativeFallbackJoinsAcrossOldThreeBlockBoundary(t *testing.T) {
	segments := []Segment{
		{ID: "e-0001", Text: strings.Repeat("Earlier explanation. ", 28)},
		{ID: "e-0002", Text: strings.Repeat("More explanation. ", 27)},
		{ID: "e-0003", Text: "A matrix is just a."},
		{ID: "e-0004", Text: "Rectangular array of numbers. Here it's two rows and two columns."},
	}
	got := buildReading(segments, conservativeBoundaries(segments), "conservative", "model_http_403", ModelMeta{})
	if len(got.Paragraphs) < 2 {
		t.Fatalf("expected paragraph split: %+v", got.Paragraphs)
	}
	found := false
	for _, p := range got.Paragraphs {
		if strings.Contains(p.Text, "A matrix is just a rectangular array of numbers.") {
			found = true
			if !strings.Contains(strings.Join(p.IDs, ","), "e-0003,e-0004") {
				t.Fatal(p.IDs)
			}
		}
	}
	if !found {
		t.Fatalf("sentence still cut: %+v", got.Paragraphs)
	}
}
func TestSafePunctuation(t *testing.T) {
	if got := trimFalseSentenceEnd("Dr."); got != "Dr." {
		t.Fatal(got)
	}
	if got := trimFalseSentenceEnd("U.S."); got != "U.S." {
		t.Fatal(got)
	}
	if got := trimFalseSentenceEnd("matrix A."); got != "matrix A." {
		t.Fatal(got)
	}
	if got := trimFalseSentenceEnd("wait..."); got != "wait..." {
		t.Fatal(got)
	}
	if got := trimFalseSentenceEnd("Really?"); got != "Really?" {
		t.Fatal(got)
	}
	if obviousJoin("matrix A.", "Rectangular array") {
		t.Fatal("variable A mistaken for article")
	}
	if got := trimFalseSentenceEnd("18.06."); got != "18.06" {
		t.Fatal(got)
	}
	if got := lowerInitialWord("A matrix"); got != "A matrix" {
		t.Fatal(got)
	}
	if got := lowerInitialWord("MATLAB codes"); got != "MATLAB codes" {
		t.Fatal(got)
	}
	if got := lowerInitialWord("Rectangular array"); got != "rectangular array" {
		t.Fatal(got)
	}
	if got := readingSeparator("这是", "矩阵"); got != "" {
		t.Fatal(got)
	}
}
func TestReadingProviderAndFallback(t *testing.T) {
	for _, mode := range []string{"ready", "forbidden", "invalid", "truncated", "model_missed_obvious"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/chat/completions" {
					t.Errorf("path: %s", r.URL.Path)
				}
				if mode == "forbidden" {
					w.WriteHeader(403)
					return
				}
				raw := joinedFixture
				if mode == "invalid" {
					raw = strings.Replace(raw, "e-0002", "unknown", 1)
				}
				if mode == "model_missed_obvious" {
					raw = strings.Replace(strings.Replace(raw, `"join_sentence":true`, `"join_sentence":false`, 1), `"lowercase_next":true`, `"lowercase_next":false`, 1)
				}
				payload, _ := json.Marshal(map[string]any{"id": "local-test", "choices": []any{map[string]any{"delta": map[string]string{"content": raw}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\n", payload)
				if mode != "truncated" {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			got := organizeReading(context.Background(), srv.URL, "fake-local-key", readingFixture())
			expected := "conservative"
			if mode == "ready" || mode == "model_missed_obvious" {
				expected = "ready"
			}
			if got.Status != expected || calls != 1 {
				t.Fatalf("result: %+v calls: %d", got, calls)
			}
			if got.Paragraphs[0].Text != "A matrix is just a rectangular array of numbers." {
				t.Fatalf("sentence still cut: %+v", got)
			}
			if mode == "forbidden" && got.ErrorCode != "model_http_403" {
				t.Fatalf("no failure reason: %+v", got)
			}
		})
	}
}

// Explicitly enabled smoke test; it calls the configured text model once.
func TestLiveReadingSmoke(t *testing.T) {
	if os.Getenv("BAILIAN_LIVE_READING_TEST") != "1" {
		t.Skip("live model test disabled")
	}
	if err := godotenv.Load(); err != nil {
		t.Fatal("cannot load demo .env")
	}
	key, base := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("BAILIAN_BASE_URL")
	if key == "" || base == "" {
		t.Fatal("model configuration missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	got := organizeReading(ctx, base, key, readingFixture())
	if got.Status != "ready" {
		t.Fatalf("reading model failed: status=%s code=%s request_id=%s", got.Status, got.ErrorCode, got.Request.ID)
	}
	if got.Paragraphs[0].Text != "A matrix is just a rectangular array of numbers." {
		t.Fatalf("unexpected reading: %q", got.Paragraphs[0].Text)
	}
}
