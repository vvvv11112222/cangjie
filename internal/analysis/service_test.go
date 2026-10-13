package analysis

import "testing"

func TestCompatibleMediaLimitsAreStableAcrossConfigurationChanges(t *testing.T) {
	got := compatibleMediaLimits(mediaLimits{})
	if got.MaxDurationMS != 7_200_000 || got.MaxVideoHeight != 1080 {
		t.Fatalf("legacy limits changed: %#v", got)
	}

	pinned := mediaLimits{MaxDurationMS: 90_000, MaxVideoHeight: 720}
	if got = compatibleMediaLimits(pinned); got != pinned {
		t.Fatalf("pinned limits were overwritten: got %#v want %#v", got, pinned)
	}
}
