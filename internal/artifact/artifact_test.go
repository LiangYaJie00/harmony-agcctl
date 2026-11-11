package artifact

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
)

func TestResolveRejectsSymlinkOutsideTask(t *testing.T) {
	root := t.TempDir()
	task := filepath.Join(root, "PUBLISH-1")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.app")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(task, "qianshou.app")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "PUBLISH-1", link); err == nil {
		t.Fatal("expected an error")
	} else if err.(*apperr.Error).Code != "ARTIFACT_SYMLINK_REJECTED" {
		t.Fatalf("error = %#v", err)
	}
}

func TestResolveAcceptsFileInsideTask(t *testing.T) {
	root := t.TempDir()
	task := filepath.Join(root, "PUBLISH-1")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(task, "qianshou.app")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(root, "PUBLISH-1", path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(path)
	if resolved != want {
		t.Fatalf("resolved = %s want = %s", resolved, want)
	}
}
