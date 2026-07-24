# Advanced + Excellent Developer A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement all remaining Developer A features: 18 new FTP commands, 13 new FS operations, PORT active mode, ABOR, graceful shutdown, handler extraction, and 14 integration tests.

**Architecture:** TCP control channel with expanded command dispatch. Handler functions extracted to `cmd/server/handlers.go`. FS operations in `internal/fs/ops.go`. PORT mode creates new UDP socket and uses `Connect()` to initiate handshake to client. ABOR uses per-transfer context cancellation.

**Tech Stack:** Go 1.21, standard library only (`net`, `crypto/sha256`, `hash/crc32`, `encoding/binary`, `sync`, `time`, `os`, `fmt`, `io`, `bufio`, `strings`, `strconv`, `log`, `flag`, `path/filepath`, `context`, `errors`, `math/rand`).

## Global Constraints

- NO external libraries — standard library imports only
- Every error checked with `fmt.Errorf("context: %w", err)` wrapping
- No `panic()` in library code — only in `main()` for irrecoverable startup failures
- Log with `log.Printf()` and prefixes: `[CTRL]`, `[RDT]`, `[FS]`, `[SESS]`, `[AUTH]`
- All shared maps protected by `sync.RWMutex`; channels for cross-goroutine signaling
- Test files alongside source as `*_test.go`
- Commit messages: `type(scope): short description` (e.g., `feat(session): add List() method`)

---

## Task 1: Session Struct Expansion

**Files:**
- Modify: `internal/session/manager.go`
- Modify: `internal/session/manager_test.go`

**Interfaces:**
- Produces: `DataMode`, `DataAddr`, `RnfrPath`, `TransferCtx`, `TransferCancel` fields on Session
- Produces: `(*Manager).List() []*Session`

- [ ] **Step 1: Write failing test for List()**

Add to `internal/session/manager_test.go`:

```go
func TestList(t *testing.T) {
	m := NewManager()
	conn1, _ := net.Pipe()
	defer conn1.Close()
	conn2, _ := net.Pipe()
	defer conn2.Close()

	s1 := m.Register(conn1)
	s2 := m.Register(conn2)

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("List() returned %d sessions, want 2", len(list))
	}

	found := map[uint32]bool{}
	for _, s := range list {
		found[s.ID] = true
	}
	if !found[s1.ID] || !found[s2.ID] {
		t.Error("List() should contain both registered sessions")
	}
}

func TestList_Empty(t *testing.T) {
	m := NewManager()
	list := m.List()
	if len(list) != 0 {
		t.Errorf("List() on empty manager returned %d sessions", len(list))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -v -run TestList`
Expected: FAIL — `m.List undefined`

- [ ] **Step 3: Add new fields and List() method**

In `internal/session/manager.go`, update the Session struct:

```go
type Session struct {
	ID             uint32
	Conn           net.Conn
	Username       string
	Authenticated  bool
	CWD            string
	DataConn       *net.UDPConn
	DataMode       string
	DataAddr       *net.UDPAddr
	TransferType   byte
	RnfrPath       string
	TransferCtx    context.Context
	TransferCancel context.CancelFunc
	Ctx            context.Context
	Cancel         context.CancelFunc
}
```

Add `List()` method after `Get()`:

```go
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0, len(m.registry))
	for _, s := range m.registry {
		sessions = append(sessions, s)
	}
	return sessions
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/session/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/session/manager.go internal/session/manager_test.go
git commit -m "feat(session): add DataMode, DataAddr, RnfrPath, TransferCtx fields and List() method"
```

---

## Task 2: Command Parser — Add 18 Commands

**Files:**
- Modify: `internal/tcpctrl/commands.go`
- Modify: `internal/tcpctrl/commands_test.go`

**Interfaces:**
- Produces: 18 new `Cmd` constants mapped in `commandNames`

- [ ] **Step 1: Add test cases for new commands**

Add to the `tests` slice in `TestParseCommand`:

```go
{"NOOP", CmdNOOP, ""},
{"CDUP", CmdCDUP, ""},
{"MKD newdir", CmdMKD, "newdir"},
{"RMD olddir", CmdRMD, "olddir"},
{"NLST", CmdNLST, ""},
{"STAT", CmdSTAT, ""},
{"SIZE file.txt", CmdSIZE, "file.txt"},
{"MDTM file.txt", CmdMDTM, "file.txt"},
{"MODE S", CmdMODE, "S"},
{"PORT 192,168,1,1,10,20", CmdPORT, "192,168,1,1,10,20"},
{"STOU", CmdSTOU, ""},
{"APPE file.txt", CmdAPPE, "file.txt"},
{"DELE file.txt", CmdDELE, "file.txt"},
{"RNFR old.txt", CmdRNFR, "old.txt"},
{"RNTO new.txt", CmdRNTO, "new.txt"},
{"HASH file.txt", CmdHASH, "file.txt"},
{"ABOR", CmdABOR, ""},
{"HELP", CmdHELP, ""},
{"HELP LIST", CmdHELP, "LIST"},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tcpctrl/ -v -run TestParseCommand`
Expected: compilation error — undefined `CmdNOOP`, etc.

- [ ] **Step 3: Add new command constants**

Replace the enum and map in `internal/tcpctrl/commands.go`:

```go
const (
	CmdUSER Cmd = iota
	CmdPASS
	CmdQUIT
	CmdTYPE
	CmdPASV
	CmdRETR
	CmdSTOR
	CmdPWD
	CmdLIST
	CmdCWD
	CmdNOOP
	CmdCDUP
	CmdMKD
	CmdRMD
	CmdNLST
	CmdSTAT
	CmdSIZE
	CmdMDTM
	CmdMODE
	CmdPORT
	CmdSTOU
	CmdAPPE
	CmdDELE
	CmdRNFR
	CmdRNTO
	CmdHASH
	CmdABOR
	CmdHELP
	CmdUNKNOWN
)

var commandNames = map[string]Cmd{
	"USER": CmdUSER,
	"PASS": CmdPASS,
	"QUIT": CmdQUIT,
	"TYPE": CmdTYPE,
	"PASV": CmdPASV,
	"RETR": CmdRETR,
	"STOR": CmdSTOR,
	"PWD":  CmdPWD,
	"LIST": CmdLIST,
	"CWD":  CmdCWD,
	"NOOP": CmdNOOP,
	"CDUP": CmdCDUP,
	"MKD":  CmdMKD,
	"RMD":  CmdRMD,
	"NLST": CmdNLST,
	"STAT": CmdSTAT,
	"SIZE": CmdSIZE,
	"MDTM": CmdMDTM,
	"MODE": CmdMODE,
	"PORT": CmdPORT,
	"STOU": CmdSTOU,
	"APPE": CmdAPPE,
	"DELE": CmdDELE,
	"RNFR": CmdRNFR,
	"RNTO": CmdRNTO,
	"HASH": CmdHASH,
	"ABOR": CmdABOR,
	"HELP": CmdHELP,
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tcpctrl/ -v -run TestParseCommand`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tcpctrl/commands.go internal/tcpctrl/commands_test.go
git commit -m "feat(tcpctrl): add 18 new FTP command constants"
```

---

## Task 3: Reply Codes — Add 8 Codes

**Files:**
- Modify: `internal/tcpctrl/replies.go`
- Modify: `internal/tcpctrl/replies_test.go`

**Interfaces:**
- Produces: 8 new reply code constants

- [ ] **Step 1: Add new constants to uniqueness test**

Add to the `codes` map in `TestReplyConstants_Unique`:

```go
ReplySystemStatus:       true,
ReplyDirStatus:          true,
ReplyFileStatus:         true,
ReplyHelp:               true,
ReplyPendingRNTO:        true,
ReplyParamError:         true,
ReplyNotImplCmd:         true,
ReplyFilenameNotAllowed: true,
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tcpctrl/ -v -run TestReplyConstants`
Expected: compilation error

- [ ] **Step 3: Add new reply codes**

Add to the `const` block in `internal/tcpctrl/replies.go`:

```go
ReplySystemStatus       = 211
ReplyDirStatus          = 212
ReplyFileStatus         = 213
ReplyHelp               = 214
ReplyPendingRNTO        = 350
ReplyParamError         = 501
ReplyNotImplCmd         = 502
ReplyFilenameNotAllowed = 553
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tcpctrl/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tcpctrl/replies.go internal/tcpctrl/replies_test.go
git commit -m "feat(tcpctrl): add 8 new reply code constants"
```

---

## Task 4: FS Operations — Directory Ops

**Files:**
- Modify: `internal/fs/ops.go`
- Create: `internal/fs/ops_test.go`

**Interfaces:**
- Consumes: `session.Session` (existing), `ResolveSafe` (existing)
- Produces: `ChangeDir`, `MakeDir`, `RemoveDir`

- [ ] **Step 1: Write failing tests**

Create `internal/fs/ops_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fs/ -v -run "TestChangeDir|TestMakeDir|TestRemoveDir"`
Expected: compilation error

- [ ] **Step 3: Implement ChangeDir, MakeDir, RemoveDir**

Add to `internal/fs/ops.go`:

```go
func ChangeDir(root string, sess *session.Session, path string) error {
	fullPath, err := ResolveSafe(root, sess, path)
	if err != nil {
		return fmt.Errorf("change dir: %w", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("change dir stat: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	sess.CWD = ResolvePath(sess, path)
	log.Printf("[FS] cwd changed to %s", sess.CWD)
	return nil
}

func MakeDir(root string, sess *session.Session, dirname string) error {
	fullPath, err := ResolveSafe(root, sess, dirname)
	if err != nil {
		return fmt.Errorf("make dir: %w", err)
	}
	if err := os.Mkdir(fullPath, 0755); err != nil {
		return fmt.Errorf("make dir: %w", err)
	}
	log.Printf("[FS] created directory %s", fullPath)
	return nil
}

func RemoveDir(root string, sess *session.Session, dirname string) error {
	fullPath, err := ResolveSafe(root, sess, dirname)
	if err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}
	log.Printf("[FS] removed directory %s", fullPath)
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fs/ -v -run "TestChangeDir|TestMakeDir|TestRemoveDir"`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/ops.go internal/fs/ops_test.go
git commit -m "feat(fs): add ChangeDir, MakeDir, RemoveDir operations"
```

---

## Task 5: FS Operations — File Metadata & Deletion

**Files:**
- Modify: `internal/fs/ops.go`
- Modify: `internal/fs/ops_test.go`

**Interfaces:**
- Produces: `DeleteFile`, `NameList`, `FileSize`, `ModTime`, `StatPath`, `OpenForAppend`

- [ ] **Step 1: Write failing tests**

Add to `internal/fs/ops_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fs/ -v -run "TestDeleteFile|TestNameList|TestFileSize|TestModTime|TestOpenForAppend"`
Expected: compilation error

- [ ] **Step 3: Implement functions**

Add to `internal/fs/ops.go`:

```go
func DeleteFile(root string, sess *session.Session, filename string) error {
	fullPath, err := ResolveSafe(root, sess, filename)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	log.Printf("[FS] deleted %s", fullPath)
	return nil
}

func NameList(dirPath string) ([]string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("name list %s: %w", dirPath, err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	log.Printf("[FS] name list %s: %d entries", dirPath, len(names))
	return names, nil
}

func FileSize(filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("file size: %w", err)
	}
	if info.IsDir() {
		return 0, fmt.Errorf("cannot SIZE a directory: %s", filePath)
	}
	return info.Size(), nil
}

func ModTime(filePath string) (time.Time, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("mod time: %w", err)
	}
	return info.ModTime(), nil
}

func StatPath(root string, sess *session.Session, path string) (string, error) {
	fullPath, err := ResolveSafe(root, sess, path)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	var result string
	if info.IsDir() {
		result = fmt.Sprintf("Directory: %s\r\nPermissions: %s\r\nModified: %s\r\n",
			info.Name(), info.Mode(), info.ModTime().Format("20060102150405"))
	} else {
		result = fmt.Sprintf("File: %s\r\nSize: %d bytes\r\nPermissions: %s\r\nModified: %s\r\n",
			info.Name(), info.Size(), info.Mode(), info.ModTime().Format("20060102150405"))
	}
	return result, nil
}

func OpenForAppend(filePath string) (*os.File, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir for append: %w", err)
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open for append: %w", err)
	}
	log.Printf("[FS] opened %s for append", filePath)
	return f, nil
}
```

Add `"time"` to the import block.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fs/ -v -run "TestDeleteFile|TestNameList|TestFileSize|TestModTime|TestOpenForAppend"`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/ops.go internal/fs/ops_test.go
git commit -m "feat(fs): add DeleteFile, NameList, FileSize, ModTime, StatPath, OpenForAppend"
```

---

## Task 6: FS Operations — Rename, Unique Name, Hash

**Files:**
- Modify: `internal/fs/ops.go`
- Modify: `internal/fs/ops_test.go`

**Interfaces:**
- Produces: `RenameFrom`, `RenameTo`, `GenerateUniqueName`, `ComputeHash`

- [ ] **Step 1: Write failing tests**

Add to `internal/fs/ops_test.go`:

```go
func TestRenameFrom_Valid(t *testing.T) {
	root, sess := setupTestRoot(t)
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("data"), 0644)

	err := RenameFrom(root, sess, "old.txt")
	if err != nil {
		t.Fatalf("RenameFrom: %v", err)
	}
	if sess.RnfrPath == "" {
		t.Error("RnfrPath should be set after RenameFrom")
	}
}

func TestRenameFrom_Nonexistent(t *testing.T) {
	root, sess := setupTestRoot(t)
	err := RenameFrom(root, sess, "nope.txt")
	if err == nil {
		t.Error("RenameFrom nonexistent should fail")
	}
}

func TestRenameTo_Valid(t *testing.T) {
	root, sess := setupTestRoot(t)
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("data"), 0644)

	RenameFrom(root, sess, "old.txt")
	err := RenameTo(root, sess, "new.txt")
	if err != nil {
		t.Fatalf("RenameTo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); err != nil {
		t.Error("new file should exist after rename")
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Error("old file should not exist after rename")
	}
	if sess.RnfrPath != "" {
		t.Error("RnfrPath should be cleared after RenameTo")
	}
}

func TestRenameTo_NoRNFR(t *testing.T) {
	root, sess := setupTestRoot(t)
	err := RenameTo(root, sess, "new.txt")
	if err == nil {
		t.Error("RenameTo without RNFR should fail")
	}
}

func TestGenerateUniqueName(t *testing.T) {
	n1 := GenerateUniqueName("photo.jpg")
	n2 := GenerateUniqueName("photo.jpg")
	if n1 == n2 {
		t.Error("GenerateUniqueName should produce unique names")
	}
	if filepath.Ext(n1) != ".jpg" {
		t.Errorf("extension should be .jpg, got %s", filepath.Ext(n1))
	}
}

func TestComputeHash(t *testing.T) {
	f := filepath.Join(t.TempDir(), "hash.txt")
	os.WriteFile(f, []byte("hello\n"), 0644)

	hash, err := ComputeHash(f)
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	expected := "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if hash != expected {
		t.Errorf("hash = %s, want %s", hash, expected)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fs/ -v -run "TestRename|TestGenerateUnique|TestComputeHash"`
Expected: compilation error

- [ ] **Step 3: Implement functions**

Add to `internal/fs/ops.go`:

```go
func RenameFrom(root string, sess *session.Session, oldname string) error {
	fullPath, err := ResolveSafe(root, sess, oldname)
	if err != nil {
		return fmt.Errorf("rename from: %w", err)
	}
	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("rename from: %w", err)
	}
	sess.RnfrPath = fullPath
	log.Printf("[FS] rename from: %s", fullPath)
	return nil
}

func RenameTo(root string, sess *session.Session, newname string) error {
	if sess.RnfrPath == "" {
		return fmt.Errorf("no RNFR path set")
	}
	fullPath, err := ResolveSafe(root, sess, newname)
	if err != nil {
		return fmt.Errorf("rename to: %w", err)
	}
	if err := os.Rename(sess.RnfrPath, fullPath); err != nil {
		return fmt.Errorf("rename to: %w", err)
	}
	log.Printf("[FS] renamed %s to %s", sess.RnfrPath, fullPath)
	sess.RnfrPath = ""
	return nil
}

func GenerateUniqueName(originalName string) string {
	ext := filepath.Ext(originalName)
	return fmt.Sprintf("stou_%d_%08x%s", time.Now().Unix(), rand.Uint32(), ext)
}

func ComputeHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("compute hash: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("compute hash: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
```

Add `"crypto/sha256"`, `"io"`, and `"math/rand"` to the import block.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fs/ -v -run "TestRename|TestGenerateUnique|TestComputeHash"`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/ops.go internal/fs/ops_test.go
git commit -m "feat(fs): add RenameFrom, RenameTo, GenerateUniqueName, ComputeHash"
```

---

## Task 7: Extract Existing Handlers to `handlers.go`

**Files:**
- Create: `cmd/server/handlers.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Produces: `handleUSER`, `handlePASS`, `handleQUIT`, `handlePWD`, `handleCWD`, `handleTYPE`, `handlePASV`, `handleLIST`, `handleRETR`, `handleSTOR`

- [ ] **Step 1: Create handlers.go with extracted handlers**

Create `cmd/server/handlers.go` with all existing handler logic moved from `main.go`. Each handler signature:

```go
func handleUSER(conn net.Conn, sess *session.Session, usersPath string, args string)
func handlePASS(conn net.Conn, sess *session.Session, usersPath string, args string)
func handleQUIT(conn net.Conn, sess *session.Session) bool  // returns true to signal exit
func handlePWD(conn net.Conn, sess *session.Session, root string)
func handleCWD(conn net.Conn, sess *session.Session, root string, args string)
func handleTYPE(conn net.Conn, sess *session.Session, args string)
func handlePASV(conn net.Conn, sess *session.Session, args string)
func handleLIST(conn net.Conn, sess *session.Session, root string, args string)
func handleRETR(conn net.Conn, sess *session.Session, root string, args string)
func handleSTOR(conn net.Conn, sess *session.Session, root string, args string)
```

Move the body of each `case` from the switch in `main.go` into the corresponding function. Add a helper:

```go
func setupDataSender(sess *session.Session) (*rdt.Sender, error) {
	if sess.DataMode == "PORT" {
		udpConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		if err != nil {
			return nil, fmt.Errorf("bind udp for PORT: %w", err)
		}
		if sess.DataConn != nil {
			sess.DataConn.Close()
		}
		sess.DataConn = udpConn
		sender := rdt.NewSender(udpConn, sess.ID)
		if err := sender.Connect(sess.DataAddr); err != nil {
			return nil, fmt.Errorf("rdt connect: %w", err)
		}
		return sender, nil
	}
	if sess.DataConn == nil {
		return nil, fmt.Errorf("use PASV or PORT first")
	}
	sender := rdt.NewSender(sess.DataConn, sess.ID)
	if err := sender.Accept(); err != nil {
		return nil, fmt.Errorf("rdt accept: %w", err)
	}
	return sender, nil
}

func setupDataReceiver(sess *session.Session) (*rdt.Receiver, error) {
	if sess.DataMode == "PORT" {
		udpConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		if err != nil {
			return nil, fmt.Errorf("bind udp for PORT: %w", err)
		}
		if sess.DataConn != nil {
			sess.DataConn.Close()
		}
		sess.DataConn = udpConn
		receiver := rdt.NewReceiver(udpConn, sess.ID)
		if err := receiver.Connect(sess.DataAddr); err != nil {
			return nil, fmt.Errorf("rdt connect: %w", err)
		}
		return receiver, nil
	}
	if sess.DataConn == nil {
		return nil, fmt.Errorf("use PASV or PORT first")
	}
	receiver := rdt.NewReceiver(sess.DataConn, sess.ID)
	if err := receiver.Accept(); err != nil {
		return nil, fmt.Errorf("rdt accept: %w", err)
	}
	return receiver, nil
}

func cleanupDataConn(sess *session.Session) {
	if sess.DataConn != nil {
		sess.DataConn.Close()
		sess.DataConn = nil
	}
	sess.DataMode = ""
}
```

- [ ] **Step 2: Update main.go dispatch to call handlers**

Replace the switch body in `handleSession` with one-liner calls:

```go
switch cmd {
case tcpctrl.CmdUSER:
    handleUSER(conn, sess, usersPath, args)
case tcpctrl.CmdPASS:
    handlePASS(conn, sess, usersPath, args)
case tcpctrl.CmdQUIT:
    if handleQUIT(conn, sess) {
        return
    }
case tcpctrl.CmdPWD:
    handlePWD(conn, sess, root)
case tcpctrl.CmdCWD:
    handleCWD(conn, sess, root, args)
case tcpctrl.CmdTYPE:
    handleTYPE(conn, sess, args)
case tcpctrl.CmdPASV:
    handlePASV(conn, sess, args)
case tcpctrl.CmdLIST:
    handleLIST(conn, sess, root, args)
case tcpctrl.CmdRETR:
    handleRETR(conn, sess, root, args)
case tcpctrl.CmdSTOR:
    handleSTOR(conn, sess, root, args)
default:
    tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "Unknown command.")
}
```

- [ ] **Step 3: Verify compilation and tests**

Run: `go build ./cmd/server/...`
Expected: no errors

Run: `go test ./...`
Expected: all existing tests PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/server/handlers.go cmd/server/main.go
git commit -m "refactor(ctrl): extract handler functions from main.go dispatch"
```

---

## Task 8: Simple Command Handlers

**Files:**
- Modify: `cmd/server/handlers.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `fs.ChangeDir`, `fs.MakeDir`, `fs.RemoveDir`, `fs.DeleteFile`, `fs.FileSize`, `fs.ModTime`, `fs.StatPath`, `fs.NameList`
- Produces: `handleNOOP`, `handleCDUP`, `handleMKD`, `handleRMD`, `handleDELE`, `handleMODE`, `handleHELP`, `handleSTAT`, `handleSIZE`, `handleMDTM`

- [ ] **Step 1: Add handler functions to handlers.go**

```go
func handleNOOP(conn net.Conn, sess *session.Session, root string, args string) {
	tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "NOOP ok.")
}

func handleCDUP(conn net.Conn, sess *session.Session, root string, args string) {
	if err := fs.ChangeDir(root, sess, ".."); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot change to parent.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory changed to parent.")
}

func handleMKD(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MKD requires a directory name.")
		return
	}
	if err := fs.MakeDir(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot create directory.")
		return
	}
	absRoot, _ := filepath.Abs(root)
	fullPath := filepath.Join(absRoot, fs.ResolvePath(sess, args))
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" created.", fullPath))
}

func handleRMD(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "RMD requires a directory name.")
		return
	}
	if err := fs.RemoveDir(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot remove directory.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory removed.")
}

func handleDELE(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "DELE requires a filename.")
		return
	}
	if err := fs.DeleteFile(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot delete file.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "File deleted.")
}

func handleMODE(conn net.Conn, sess *session.Session, root string, args string) {
	if len(args) == 0 {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MODE requires S, B, or C.")
		return
	}
	switch args[0] {
	case 'S', 's':
		tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "Mode set to Stream.")
	case 'B', 'b':
		tcpctrl.WriteReply(conn, tcpctrl.ReplyNotImpl, "Block mode not implemented.")
	case 'C', 'c':
		tcpctrl.WriteReply(conn, tcpctrl.ReplyNotImpl, "Compressed mode not implemented.")
	default:
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MODE must be S, B, or C.")
	}
}

func handleHELP(conn net.Conn, sess *session.Session, root string, args string) {
	commands := "USER PASS QUIT NOOP PWD CWD CDUP MKD RMD LIST NLST STAT SIZE MDTM TYPE MODE PORT PASV RETR STOR STOU APPE DELE RNFR RNTO HASH ABOR HELP"
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyHelp, fmt.Sprintf("Supported commands: %s", commands))
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyHelp, fmt.Sprintf("Help for %s: see RFC 959.", strings.ToUpper(args)))
}

func handleSTAT(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplySystemStatus, "Hybrid FTP server status: running.")
		return
	}
	info, err := fs.StatPath(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot stat path.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, strings.TrimRight(info, "\r\n"))
}

func handleSIZE(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "SIZE requires a filename.")
		return
	}
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	size, err := fs.FileSize(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot get file size.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, fmt.Sprintf("%d", size))
}

func handleMDTM(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MDTM requires a filename.")
		return
	}
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	mt, err := fs.ModTime(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot get modification time.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, mt.Format("20060102150405"))
}
```

- [ ] **Step 2: Add cases to main.go dispatch**

Add to the switch:

```go
case tcpctrl.CmdNOOP:
    handleNOOP(conn, sess, root, args)
case tcpctrl.CmdCDUP:
    handleCDUP(conn, sess, root, args)
case tcpctrl.CmdMKD:
    handleMKD(conn, sess, root, args)
case tcpctrl.CmdRMD:
    handleRMD(conn, sess, root, args)
case tcpctrl.CmdDELE:
    handleDELE(conn, sess, root, args)
case tcpctrl.CmdMODE:
    handleMODE(conn, sess, root, args)
case tcpctrl.CmdHELP:
    handleHELP(conn, sess, root, args)
case tcpctrl.CmdSTAT:
    handleSTAT(conn, sess, root, args)
case tcpctrl.CmdSIZE:
    handleSIZE(conn, sess, root, args)
case tcpctrl.CmdMDTM:
    handleMDTM(conn, sess, root, args)
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add cmd/server/handlers.go cmd/server/main.go
git commit -m "feat(ctrl): add NOOP, CDUP, MKD, RMD, DELE, MODE, HELP, STAT, SIZE, MDTM handlers"
```

---

## Task 9: PORT Command Handler

**Files:**
- Modify: `cmd/server/handlers.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Produces: `handlePORT` — parses `h1,h2,h3,h4,p1,p2`, stores `DataAddr` and `DataMode`

- [ ] **Step 1: Add handlePORT to handlers.go**

```go
func handlePORT(conn net.Conn, sess *session.Session, root string, args string) {
	parts := strings.Split(args, ",")
	if len(parts) != 6 {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "PORT requires h1,h2,h3,h4,p1,p2.")
		return
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "Invalid PORT parameter.")
			return
		}
		nums[i] = n
	}
	ip := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]

	sess.DataAddr = &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
	sess.DataMode = "PORT"
	if sess.DataConn != nil {
		sess.DataConn.Close()
		sess.DataConn = nil
	}
	log.Printf("[CTRL] session %d PORT %s:%d", sess.ID, ip, port)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "PORT command successful.")
}
```

Add `"strconv"` to handlers.go imports.

- [ ] **Step 2: Update handleLIST, handleRETR, handleSTOR to use setupDataSender/setupDataReceiver**

Replace the PASV-only data connection logic in `handleLIST`, `handleRETR`, and `handleSTOR` with calls to `setupDataSender(sess)` / `setupDataReceiver(sess)` and `cleanupDataConn(sess)` after transfer.

- [ ] **Step 3: Add PORT case to main.go dispatch**

```go
case tcpctrl.CmdPORT:
    handlePORT(conn, sess, root, args)
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add cmd/server/handlers.go cmd/server/main.go
git commit -m "feat(ctrl): add PORT command handler with active mode support"
```

---

## Task 10: Data Transfer Handlers — NLST, APPE, STOU, HASH

**Files:**
- Modify: `cmd/server/handlers.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `setupDataSender`, `setupDataReceiver`, `cleanupDataConn`, `fs.NameList`, `fs.OpenForAppend`, `fs.GenerateUniqueName`, `fs.ComputeHash`, `fs.OpenForWrite`

- [ ] **Step 1: Add handlers to handlers.go**

```go
func handleNLST(conn net.Conn, sess *session.Session, root string, args string) {
	path := args
	if path == "" {
		path = "."
	}
	fullPath, err := fs.ResolveSafe(root, sess, path)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	names, err := fs.NameList(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot list directory.")
		return
	}
	listing := strings.Join(names, "\r\n") + "\r\n"

	sender, err := setupDataSender(sess)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV or PORT first.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK, "Opening data connection for name list.")
	sender.Send([]byte(listing))
	sender.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Name list complete.")
}

func handleAPPE(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "APPE requires a filename.")
		return
	}
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	file, err := fs.OpenForAppend(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot open file for append.")
		return
	}
	defer file.Close()

	receiver, err := setupDataReceiver(sess)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV or PORT first.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK, fmt.Sprintf("Opening data connection for %s.", args))

	for chunk := range receiver.Receive() {
		if _, err := file.Write(chunk); err != nil {
			log.Printf("[CTRL] write error: %v", err)
			receiver.Close()
			tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
			cleanupDataConn(sess)
			return
		}
	}
	receiver.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
}

func handleSTOU(conn net.Conn, sess *session.Session, root string, args string) {
	uniqueName := fs.GenerateUniqueName("upload.dat")
	fullPath, err := fs.ResolveSafe(root, sess, uniqueName)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot create unique file.")
		return
	}
	file, err := fs.OpenForWrite(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot create file.")
		return
	}
	defer file.Close()

	receiver, err := setupDataReceiver(sess)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV or PORT first.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK, fmt.Sprintf("Opening data connection for %s.", uniqueName))

	for chunk := range receiver.Receive() {
		if _, err := file.Write(chunk); err != nil {
			log.Printf("[CTRL] write error: %v", err)
			receiver.Close()
			tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
			cleanupDataConn(sess)
			return
		}
	}
	receiver.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" stored.", uniqueName))
}

func handleHASH(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "HASH requires a filename.")
		return
	}
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	hash, err := fs.ComputeHash(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot compute hash.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, fmt.Sprintf("SHA-256 0-%s %s", hash, args))
}
```

- [ ] **Step 2: Add cases to main.go dispatch**

```go
case tcpctrl.CmdNLST:
    handleNLST(conn, sess, root, args)
case tcpctrl.CmdAPPE:
    handleAPPE(conn, sess, root, args)
case tcpctrl.CmdSTOU:
    handleSTOU(conn, sess, root, args)
case tcpctrl.CmdHASH:
    handleHASH(conn, sess, root, args)
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add cmd/server/handlers.go cmd/server/main.go
git commit -m "feat(ctrl): add NLST, APPE, STOU, HASH handlers"
```

---

## Task 11: RNFR/RNTO and ABOR Handlers

**Files:**
- Modify: `cmd/server/handlers.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `fs.RenameFrom`, `fs.RenameTo`, `sess.TransferCancel`

- [ ] **Step 1: Add handlers to handlers.go**

```go
func handleRNFR(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "RNFR requires a filename.")
		return
	}
	if err := fs.RenameFrom(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "File not found.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPendingRNTO, "File exists, ready for RNTO.")
}

func handleRNTO(conn net.Conn, sess *session.Session, root string, args string) {
	if sess.RnfrPath == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "RNFR required first.")
		return
	}
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "RNTO requires a filename.")
		return
	}
	if err := fs.RenameTo(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Rename failed.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Rename successful.")
}

func handleABOR(conn net.Conn, sess *session.Session, root string, args string) {
	if sess.TransferCancel != nil {
		sess.TransferCancel()
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "ABOR successful.")
}
```

- [ ] **Step 2: Add transfer context to handleRETR, handleSTOR, handleLIST, handleNLST, handleAPPE, handleSTOU**

Before each transfer, add:

```go
sess.TransferCtx, sess.TransferCancel = context.WithCancel(sess.Ctx)
defer func() {
    sess.TransferCancel()
    sess.TransferCancel = nil
    sess.TransferCtx = nil
}()
```

In file read/write loops, add abort check:

```go
select {
case <-sess.TransferCtx.Done():
    log.Printf("[CTRL] transfer aborted by ABOR")
    tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
    cleanupDataConn(sess)
    return
default:
}
```

- [ ] **Step 3: Add cases to main.go dispatch**

```go
case tcpctrl.CmdRNFR:
    handleRNFR(conn, sess, root, args)
case tcpctrl.CmdRNTO:
    handleRNTO(conn, sess, root, args)
case tcpctrl.CmdABOR:
    handleABOR(conn, sess, root, args)
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add cmd/server/handlers.go cmd/server/main.go
git commit -m "feat(ctrl): add RNFR, RNTO, ABOR handlers with transfer context"
```

---

## Task 12: Graceful Shutdown

**Files:**
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `mgr.List()`, `tcpctrl.WriteReply`

- [ ] **Step 1: Add WaitGroup and improved shutdown to main.go**

Replace the main function:

```go
func main() {
	flag.Parse()

	if err := os.MkdirAll(*rootDir, 0755); err != nil {
		log.Fatalf("cannot create root directory: %v", err)
	}

	mgr := session.NewManager()

	listener, err := net.Listen("tcp", *ctrlPort)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *ctrlPort, err)
	}
	log.Printf("[CTRL] server listening on %s, root=%s", *ctrlPort, *rootDir)

	var wg sync.WaitGroup

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("[CTRL] shutting down...")
		listener.Close()
		for _, s := range mgr.List() {
			tcpctrl.WriteReply(s.Conn, 421, "Service not available, closing.")
			s.Cancel()
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[CTRL] accept error: %v", err)
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleSession(conn, mgr, *rootDir, *usersFile)
		}()
	}

	wg.Wait()
	log.Printf("[CTRL] server stopped.")
}
```

Add `"sync"` to imports.

- [ ] **Step 2: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(ctrl): add graceful shutdown with WaitGroup and 421 broadcast"
```

---

## Task 13: Integration Tests — Directory & File Operations

**Files:**
- Modify: `test/integration/e2e_test.go`

**Interfaces:**
- Consumes: server binary, test helpers (`ftpConn`, `startServer`, `parsePASVReply`, `doRDTDownload`)

- [ ] **Step 1: Add test functions**

```go
func TestE2E_MKD_RMD(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "MKD testdir"); !strings.HasPrefix(r, "257") {
		t.Errorf("MKD: expected 257, got %s", r)
	}

	reply := fc.sendAndRead(t, "PASV")
	host, port, _ := parsePASVReply(reply)
	r := fc.sendAndRead(t, "LIST")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("LIST: expected 150, got %s", r)
	}
	listing := doRDTDownloadString(t, host, port)
	if !strings.Contains(listing, "testdir") {
		t.Errorf("LIST should contain testdir, got: %s", listing)
	}
	fc.readReply(t)

	if r := fc.sendAndRead(t, "RMD testdir"); !strings.HasPrefix(r, "250") {
		t.Errorf("RMD: expected 250, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_DELE(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "todelete.txt"), []byte("bye"), 0644)

	if r := fc.sendAndRead(t, "DELE todelete.txt"); !strings.HasPrefix(r, "250") {
		t.Errorf("DELE: expected 250, got %s", r)
	}

	if r := fc.sendAndRead(t, "RETR todelete.txt"); !strings.HasPrefix(r, "550") {
		t.Errorf("RETR deleted file: expected 550, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_CDUP(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.MkdirAll(filepath.Join(rootDir, "cdupdir"), 0755)

	fc.sendAndRead(t, "CWD cdupdir")
	if r := fc.sendAndRead(t, "CDUP"); !strings.HasPrefix(r, "250") {
		t.Errorf("CDUP: expected 250, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_NLST(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "alpha.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(rootDir, "beta.txt"), []byte("b"), 0644)

	reply := fc.sendAndRead(t, "PASV")
	host, port, _ := parsePASVReply(reply)

	r := fc.sendAndRead(t, "NLST")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("NLST: expected 150, got %s", r)
	}
	listing := doRDTDownloadString(t, host, port)
	if !strings.Contains(listing, "alpha.txt") || !strings.Contains(listing, "beta.txt") {
		t.Errorf("NLST missing files, got: %s", listing)
	}
	fc.readReply(t)
	fc.sendAndRead(t, "QUIT")
}

func TestE2E_SIZE_MDTM(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "sized.txt"), []byte("12345"), 0644)

	if r := fc.sendAndRead(t, "SIZE sized.txt"); !strings.HasPrefix(r, "213") || !strings.Contains(r, "5") {
		t.Errorf("SIZE: expected 213 with 5, got %s", r)
	}

	if r := fc.sendAndRead(t, "MDTM sized.txt"); !strings.HasPrefix(r, "213") {
		t.Errorf("MDTM: expected 213, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./test/integration/ -v -run "TestE2E_MKD_RMD|TestE2E_DELE|TestE2E_CDUP|TestE2E_NLST|TestE2E_SIZE_MDTM" -timeout 60s`
Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add test/integration/e2e_test.go
git commit -m "test(integration): add tests for MKD, RMD, DELE, CDUP, NLST, SIZE, MDTM"
```

---

## Task 14: Integration Tests — Rename, Hash, Mode, STOU, APPE

**Files:**
- Modify: `test/integration/e2e_test.go`

- [ ] **Step 1: Add test functions**

```go
func TestE2E_RNFR_RNTO(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "original.txt"), []byte("data"), 0644)

	if r := fc.sendAndRead(t, "RNFR original.txt"); !strings.HasPrefix(r, "350") {
		t.Fatalf("RNFR: expected 350, got %s", r)
	}
	if r := fc.sendAndRead(t, "RNTO renamed.txt"); !strings.HasPrefix(r, "250") {
		t.Errorf("RNTO: expected 250, got %s", r)
	}
	if r := fc.sendAndRead(t, "RETR original.txt"); !strings.HasPrefix(r, "550") {
		t.Errorf("RETR old name: expected 550, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_STOU(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	reply := fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("PASV: expected 227, got %s", reply)
	}
	host, port, _ := parsePASVReply(reply)

	r := fc.sendAndRead(t, "STOU")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("STOU: expected 150, got %s", r)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	defer udpConn.Close()

	sender := rdt.NewSender(udpConn, 1)
	sender.Connect(udpAddr)
	sender.Send([]byte("unique upload"))
	sender.Close()

	time.Sleep(500 * time.Millisecond)
	r2 := fc.readReply(t)
	if !strings.HasPrefix(r2, "257") {
		t.Errorf("STOU completion: expected 257, got %s", r2)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_APPE(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "append.txt"), []byte("hello"), 0644)

	reply := fc.sendAndRead(t, "PASV")
	host, port, _ := parsePASVReply(reply)

	r := fc.sendAndRead(t, "APPE append.txt")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("APPE: expected 150, got %s", r)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	defer udpConn.Close()

	sender := rdt.NewSender(udpConn, 1)
	sender.Connect(udpAddr)
	sender.Send([]byte(" world"))
	sender.Close()

	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)

	data, _ := os.ReadFile(filepath.Join(rootDir, "append.txt"))
	if string(data) != "hello world" {
		t.Errorf("append result = %q, want 'hello world'", string(data))
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_HASH(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "hash.txt"), []byte("hello\n"), 0644)

	r := fc.sendAndRead(t, "HASH hash.txt")
	if !strings.HasPrefix(r, "213") {
		t.Errorf("HASH: expected 213, got %s", r)
	}
	if !strings.Contains(r, "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03") {
		t.Errorf("HASH: wrong hash in %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_MODE(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "MODE S"); !strings.HasPrefix(r, "200") {
		t.Errorf("MODE S: expected 200, got %s", r)
	}
	if r := fc.sendAndRead(t, "MODE B"); !strings.HasPrefix(r, "504") {
		t.Errorf("MODE B: expected 504, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_NOOP_HELP(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "NOOP"); !strings.HasPrefix(r, "200") {
		t.Errorf("NOOP: expected 200, got %s", r)
	}
	if r := fc.sendAndRead(t, "HELP"); !strings.HasPrefix(r, "214") {
		t.Errorf("HELP: expected 214, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./test/integration/ -v -run "TestE2E_RNFR_RNTO|TestE2E_STOU|TestE2E_APPE|TestE2E_HASH|TestE2E_MODE|TestE2E_NOOP_HELP" -timeout 60s`
Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add test/integration/e2e_test.go
git commit -m "test(integration): add tests for RNFR/RNTO, STOU, APPE, HASH, MODE, NOOP, HELP"
```

---

## Task 15: Integration Tests — PORT Mode & Concurrent Sessions

**Files:**
- Modify: `test/integration/e2e_test.go`

- [ ] **Step 1: Add test functions**

```go
func TestE2E_PORT_Transfer(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "portfile.txt"), []byte("port mode data\n"), 0644)

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	localPort := udpConn.LocalAddr().(*net.UDPAddr).Port
	p1 := localPort / 256
	p2 := localPort % 256
	portCmd := fmt.Sprintf("PORT 127,0,0,1,%d,%d", p1, p2)

	if r := fc.sendAndRead(t, portCmd); !strings.HasPrefix(r, "200") {
		t.Fatalf("PORT: expected 200, got %s", r)
	}

	r := fc.sendAndRead(t, "RETR portfile.txt")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("RETR: expected 150, got %s", r)
	}

	sessionID := uint32(1)
	receiver := rdt.NewReceiver(udpConn, sessionID)

	recvCh := make(chan []byte, 1)
	go func() {
		receiver.Accept()
		var buf []byte
		for chunk := range receiver.Receive() {
			buf = append(buf, chunk...)
		}
		recvCh <- buf
	}()

	select {
	case data := <-recvCh:
		if !bytes.Equal(data, []byte("port mode data\n")) {
			t.Errorf("PORT download: got %q", string(data))
		}
	case <-time.After(5 * time.Second):
		receiver.Close()
		t.Error("PORT download timed out")
	}

	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)
	fc.sendAndRead(t, "QUIT")
}

func TestE2E_ConcurrentSessions(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	os.WriteFile(filepath.Join(rootDir, "shared.txt"), []byte("shared content\n"), 0644)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", ":"+ctrlPort)
			if err != nil {
				t.Errorf("session %d dial: %v", id, err)
				return
			}
			defer conn.Close()
			fc := newFTPConn(t, conn)

			fc.sendAndRead(t, "")
			fc.sendAndRead(t, "USER admin")
			fc.sendAndRead(t, "PASS admin")

			reply := fc.sendAndRead(t, "PASV")
			if !strings.HasPrefix(reply, "227") {
				t.Errorf("session %d PASV: expected 227, got %s", id, reply)
				return
			}
			host, port, _ := parsePASVReply(reply)

			r := fc.sendAndRead(t, "RETR shared.txt")
			if !strings.HasPrefix(r, "150") {
				t.Errorf("session %d RETR: expected 150, got %s", id, r)
				return
			}

			data, err := doRDTDownload(t, host, port)
			if err != nil {
				t.Errorf("session %d download: %v", id, err)
				return
			}
			if !bytes.Equal(data, []byte("shared content\n")) {
				t.Errorf("session %d: content mismatch", id)
			}

			time.Sleep(500 * time.Millisecond)
			fc.readReply(t)
			fc.sendAndRead(t, "QUIT")
		}(i)
	}
	wg.Wait()
}
```

Add `"sync"` to test file imports.

- [ ] **Step 2: Run tests**

Run: `go test ./test/integration/ -v -run "TestE2E_PORT_Transfer|TestE2E_ConcurrentSessions" -timeout 60s`
Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add test/integration/e2e_test.go
git commit -m "test(integration): add tests for PORT mode and concurrent sessions"
```

---

## Task 16: Test Fixture

**Files:**
- Create: `test/fixtures/sample.txt`

- [ ] **Step 1: Create sample.txt**

```
This is a sample text file for Hybrid FTP integration tests.
It contains multiple lines of ASCII text.
Line 3: The quick brown fox jumps over the lazy dog.
Line 4: Pack my box with five dozen liquor jugs.
Line 5: End of sample file.
```

- [ ] **Step 2: Commit**

```bash
git add test/fixtures/sample.txt
git commit -m "test(fixtures): add sample.txt test fixture"
```
