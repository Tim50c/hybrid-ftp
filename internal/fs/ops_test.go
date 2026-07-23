package fs

import (
	"os"
	"path/filepath"
	"testing"

	"hybrid-ftp/internal/session"
)

func setupTestRoot(t *testing.T) (string, *session.Session) {
	t.Helper()
	root := t.TempDir()
	sess := &session.Session{CWD: "/"}
	return root, sess
}

func TestChangeDir_Valid(t *testing.T) {
	root, sess := setupTestRoot(t)
	os.MkdirAll(filepath.Join(root, "subdir"), 0755)

	err := ChangeDir(root, sess, "subdir")
	if err != nil {
		t.Fatalf("ChangeDir: %v", err)
	}
	if sess.CWD != "/subdir" && sess.CWD != "\\subdir" {
		t.Errorf("CWD = %q, want /subdir", sess.CWD)
	}
}

func TestChangeDir_Nonexistent(t *testing.T) {
	root, sess := setupTestRoot(t)
	err := ChangeDir(root, sess, "nope")
	if err == nil {
		t.Error("ChangeDir to nonexistent should fail")
	}
}

func TestChangeDir_PathTraversal(t *testing.T) {
	root, sess := setupTestRoot(t)
	err := ChangeDir(root, sess, "../../etc")
	if err == nil {
		t.Error("ChangeDir path traversal should fail")
	}
}

func TestMakeDir_Valid(t *testing.T) {
	root, sess := setupTestRoot(t)
	err := MakeDir(root, sess, "newdir")
	if err != nil {
		t.Fatalf("MakeDir: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "newdir"))
	if err != nil || !info.IsDir() {
		t.Error("directory should exist after MakeDir")
	}
}

func TestRemoveDir_Valid(t *testing.T) {
	root, sess := setupTestRoot(t)
	os.MkdirAll(filepath.Join(root, "emptydir"), 0755)

	err := RemoveDir(root, sess, "emptydir")
	if err != nil {
		t.Fatalf("RemoveDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "emptydir")); !os.IsNotExist(err) {
		t.Error("directory should be removed")
	}
}

func TestRemoveDir_NonEmpty(t *testing.T) {
	root, sess := setupTestRoot(t)
	dir := filepath.Join(root, "notempty")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0644)

	err := RemoveDir(root, sess, "notempty")
	if err == nil {
		t.Error("RemoveDir on non-empty should fail")
	}
}
