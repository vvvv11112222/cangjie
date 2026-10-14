package media

import "testing"

func TestValidateUsesPreservesEmptyJSONList(t *testing.T) {
	uses, err := validateUses([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if uses == nil || len(uses) != 0 {
		t.Fatalf("empty uses must remain a non-nil empty list: %#v", uses)
	}
	if _, err := validateUses([]string{"playback", "playback"}); err == nil {
		t.Fatal("duplicate use was accepted")
	}
	if _, err := validateUses([]string{"download"}); err == nil {
		t.Fatal("unknown use was accepted")
	}
}

func TestContainerSignature(t *testing.T) {
	mp4 := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	mkv := []byte{0x1a, 0x45, 0xdf, 0xa3}
	if !validContainer(".mp4", mp4) || !validContainer(".mov", mp4) || !validContainer(".mkv", mkv) {
		t.Fatal("valid container signature was rejected")
	}
	if validContainer(".mp4", mkv) || validContainer(".mkv", mp4) {
		t.Fatal("mismatched container signature was accepted")
	}
}
