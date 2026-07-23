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

func TestDeleteFile_Valid(t *testing.T) {
	root, sess := setupTestRoot(t)
	os.WriteFile(filepath.Join(root, "todelete.txt"), []byte("bye"), 0644)

	err := DeleteFile(root, sess, "todelete.txt")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "todelete.txt")); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestDeleteFile_Nonexistent(t *testing.T) {
	root, sess := setupTestRoot(t)
	err := DeleteFile(root, sess, "nope.txt")
	if err == nil {
		t.Error("DeleteFile nonexistent should fail")
	}
}

func TestNameList(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)

	names, err := NameList(dir)
	if err != nil {
		t.Fatalf("NameList: %v", err)
	}
	if len(names) != 3 {
		t.Errorf("NameList returned %d names, want 3", len(names))
	}
}

func TestFileSize(t *testing.T) {
	f := filepath.Join(t.TempDir(), "sized.txt")
	os.WriteFile(f, []byte("12345"), 0644)

	size, err := FileSize(f)
	if err != nil {
		t.Fatalf("FileSize: %v", err)
	}
	if size != 5 {
		t.Errorf("FileSize = %d, want 5", size)
	}
}

func TestModTime(t *testing.T) {
	f := filepath.Join(t.TempDir(), "timed.txt")
	os.WriteFile(f, []byte("x"), 0644)

	mt, err := ModTime(f)
	if err != nil {
		t.Fatalf("ModTime: %v", err)
	}
	if mt.IsZero() {
		t.Error("ModTime should not be zero")
	}
}

func TestOpenForAppend_Existing(t *testing.T) {
	f := filepath.Join(t.TempDir(), "append.txt")
	os.WriteFile(f, []byte("hello"), 0644)

	file, err := OpenForAppend(f)
	if err != nil {
		t.Fatalf("OpenForAppend: %v", err)
	}
	defer file.Close()
	file.Write([]byte(" world"))
	file.Close()

	data, _ := os.ReadFile(f)
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want 'hello world'", string(data))
	}
}

func TestOpenForAppend_New(t *testing.T) {
	f := filepath.Join(t.TempDir(), "new.txt")
	file, err := OpenForAppend(f)
	if err != nil {
		t.Fatalf("OpenForAppend new: %v", err)
	}
	defer file.Close()
	file.Write([]byte("created"))
	file.Close()

	data, _ := os.ReadFile(f)
	if string(data) != "created" {
		t.Errorf("file content = %q, want 'created'", string(data))
	}
}
