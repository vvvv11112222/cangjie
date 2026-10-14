package httpapi

import "testing"

func TestParseByteRange(t *testing.T) {
	tests := []struct {
		header              string
		start, length       int64
		partial, shouldFail bool
	}{
		{"", 0, 10, false, false},
		{"bytes=2-5", 2, 4, true, false},
		{"bytes=7-", 7, 3, true, false},
		{"bytes=-3", 7, 3, true, false},
		{"bytes=2-99", 2, 8, true, false},
		{"bytes=10-", 0, 0, false, true},
		{"bytes=1-2,4-5", 0, 0, false, true},
		{"items=1-2", 0, 0, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			start, length, partial, err := parseByteRange(tc.header, 10)
			if (err != nil) != tc.shouldFail || start != tc.start || length != tc.length || partial != tc.partial {
				t.Fatalf("got start=%d length=%d partial=%v err=%v", start, length, partial, err)
			}
		})
	}
}
