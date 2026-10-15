package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordingPathRejectsEscapeDirectoryAndLink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(root, "part.mp4")
	if e := os.WriteFile(file, []byte("part"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := checkedFile(root, file); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{root, filepath.Join(outside, "part.mp4"), filepath.Join(root, "..", "outside.mp4")} {
		if _, e := checkedFile(root, p); e == nil {
			t.Errorf("accepted unsafe path %s", p)
		}
	}
	link := filepath.Join(root, "link.mp4")
	if e := os.Symlink(file, link); e == nil {
		if _, e = checkedFile(root, link); e == nil {
			t.Fatal("accepted symlink")
		}
	}
}
