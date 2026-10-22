package media

import (
	"strings"
	"testing"
)

func TestDisplayNameDoesNotExposePathsOrControls(t *testing.T) {
	for input, want := range map[string]string{`C:\private\course\课堂.mp4`: "课堂.mp4", "/home/private/lesson.mp4": "lesson.mp4", "a\n\r\t.mp4": "a.mp4", ".": "", "": ""} {
		if got := cleanDisplayName(input); got != want {
			t.Errorf("%q => %q, want %q", input, got, want)
		}
	}
	if got := cleanDisplayName(strings.Repeat("课", 300) + ".mp4"); len([]rune(got)) != 255 {
		t.Fatal("name was not bounded by Unicode characters")
	}
}
