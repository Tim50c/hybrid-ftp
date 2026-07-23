# Basic Level Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the working Basic Level of Hybrid FTP: authentication, PASV-only data channel, Selective Repeat ARQ over UDP, single-file ASCII upload/download.

**Architecture:** TCP control channel (FTP commands/replies) + custom RDT over UDP data channel with Selective Repeat and CRC32 checksums. No BBR — fixed cwnd of 28000 bytes.

**Tech Stack:** Go 1.21, standard library only (`net`, `crypto/sha256`, `hash/crc32`, `encoding/binary`, `sync`, `time`, `os`, `fmt`, `io`, `bufio`, `strings`, `strconv`, `log`, `flag`, `path/filepath`, `context`, `errors`).

## Global Constraints

- NO external libraries — standard library imports only
- Every error must be checked with `fmt.Errorf("context: %w", err)` wrapping
- No `panic()` in library code — only in `main()` for irrecoverable startup failures
- Log with `log.Printf()` and consistent prefixes: `[CTRL]`, `[RDT]`, `[FS]`, `[SESS]`, `[AUTH]`
- All shared maps protected by `sync.RWMutex`; channels for cross-goroutine signaling
- Test files alongside source as `*_test.go`
- Commit messages format: `[scope] brief description`

---

## Task 1: Credentials File

**Files:**
- Create: `users.conf`

**Interfaces:**
- Produces: credentials file consumed by `auth.CheckPassword()`

- [ ] **Step 1: Create users.conf**

Write file `users.conf`:

```
# Hybrid FTP user credentials
# Format: username:SHA-256-hex-hash
# Password for admin: admin
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
# Password for user1: user
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
```

Verify with: `echo -n "admin" | sha256sum` (should match `8c6976e5...`)

- [ ] **Step 2: Commit**

```bash
git add users.conf
git commit -m "[config] add users.conf with SHA-256 hashed credentials"
```

---

## Task 2: Command Parser

**Files:**
- Create: `internal/tcpctrl/commands.go`
- Create: `internal/tcpctrl/commands_test.go`

**Interfaces:**
- Produces: `type Cmd int` (enum), `ParseCommand(line string) (Cmd, string)`

- [ ] **Step 1: Write the failing test**

Create `internal/tcpctrl/commands_test.go`:

```go
package tcpctrl

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		line    string
		wantCmd Cmd
		wantArg string
	}{
		{"USER admin", CmdUSER, "admin"},
		{"PASS secret", CmdPASS, "secret"},
		{"QUIT", CmdQUIT, ""},
		{"TYPE A", CmdTYPE, "A"},
		{"PASV", CmdPASV, ""},
		{"RETR file.txt", CmdRETR, "file.txt"},
		{"STOR new.txt", CmdSTOR, "new.txt"},
		{"PWD", CmdPWD, ""},
		{"LIST /tmp", CmdLIST, "/tmp"},
		{"CWD subdir", CmdCWD, "subdir"},
		{"unknown command", CmdUNKNOWN, "unknown command"},
		{"", CmdUNKNOWN, ""},
		{"user lowercase", CmdUSER, "lowercase"},
		{"paSs mixed", CmdPASS, "mixed"},
	}
	for _, tt := range tests {
		cmd, arg := ParseCommand(tt.line)
		if cmd != tt.wantCmd {
			t.Errorf("ParseCommand(%q) cmd = %v, want %v", tt.line, cmd, tt.wantCmd)
		}
		if arg != tt.wantArg {
			t.Errorf("ParseCommand(%q) arg = %q, want %q", tt.line, arg, tt.wantArg)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tcpctrl/ -v -run TestParseCommand`
Expected: compilation error (undefined: Cmd, ParseCommand)

- [ ] **Step 3: Write minimal implementation**

Create `internal/tcpctrl/commands.go`:

```go
package tcpctrl

import "strings"

type Cmd int

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
}

func ParseCommand(line string) (Cmd, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return CmdUNKNOWN, ""
	}
	parts := strings.SplitN(line, " ", 2)
	name := strings.ToUpper(parts[0])
	cmd, ok := commandNames[name]
	if !ok {
		if len(parts) > 1 {
			return CmdUNKNOWN, line
		}
		return CmdUNKNOWN, parts[0]
	}
	if len(parts) > 1 {
		return cmd, parts[1]
	}
	return cmd, ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tcpctrl/ -v -run TestParseCommand`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tcpctrl/commands.go internal/tcpctrl/commands_test.go
git commit -m "[tcpctrl] add command enum and parser"
```

---

## Task 3: Reply Codes and Formatting

**Files:**
- Create: `internal/tcpctrl/replies.go`
- Create: `internal/tcpctrl/replies_test.go`

**Interfaces:**
- Produces: reply code constants, `FormatReply(code int, msg string) string`

- [ ] **Step 1: Write the failing test**

Create `internal/tcpctrl/replies_test.go`:

```go
package tcpctrl

import "testing"

func TestFormatReply(t *testing.T) {
	tests := []struct {
		code int
		msg  string
		want string
	}{
		{220, "Hybrid FTP Ready.", "220 Hybrid FTP Ready.\r\n"},
		{331, "Password required.", "331 Password required.\r\n"},
		{230, "Login successful.", "230 Login successful.\r\n"},
		{150, "Opening data connection.", "150 Opening data connection.\r\n"},
		{226, "Transfer complete.", "226 Transfer complete.\r\n"},
		{500, "Syntax error.", "500 Syntax error.\r\n"},
	}
	for _, tt := range tests {
		got := FormatReply(tt.code, tt.msg)
		if got != tt.want {
			t.Errorf("FormatReply(%d, %q) = %q, want %q", tt.code, tt.msg, got, tt.want)
		}
	}
}

func TestReplyConstants_Unique(t *testing.T) {
	codes := map[int]bool{
		ReplyReady:        true,
		ReplyLoggedIn:     true,
		ReplyNeedPassword: true,
		ReplyNotLoggedIn:  true,
		ReplyOK:           true,
		ReplyFileOK:       true,
		ReplyClosingData:  true,
		ReplyPASV:         true,
		ReplyQuit:         true,
		ReplyFileActionOK: true,
		ReplyPathCreated:  true,
		ReplySyntaxError:  true,
		ReplyBadSequence:  true,
		ReplyNotImpl:      true,
		ReplyFileUnavail:  true,
		ReplyCantOpenData: true,
		ReplyTransferAborted: true,
	}
	for c := range codes {
		if codes[c] != true {
			t.Errorf("duplicate reply code: %d", c)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tcpctrl/ -v -run TestFormatReply`
Expected: compilation error (undefined: FormatReply, etc.)

- [ ] **Step 3: Write minimal implementation**

Create `internal/tcpctrl/replies.go`:

```go
package tcpctrl

import "fmt"

const (
	ReplyReady            = 220
	ReplyLoggedIn         = 230
	ReplyNeedPassword     = 331
	ReplyNotLoggedIn      = 530
	ReplyOK               = 200
	ReplyFileOK           = 150
	ReplyClosingData      = 226
	ReplyPASV             = 227
	ReplyQuit             = 221
	ReplyFileActionOK     = 250
	ReplyPathCreated      = 257
	ReplySyntaxError      = 500
	ReplyBadSequence      = 503
	ReplyNotImpl          = 504
	ReplyFileUnavail      = 550
	ReplyCantOpenData     = 425
	ReplyTransferAborted  = 426
)

func FormatReply(code int, msg string) string {
	return fmt.Sprintf("%d %s\r\n", code, msg)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tcpctrl/ -v -run TestFormatReply`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tcpctrl/replies.go internal/tcpctrl/replies_test.go
git commit -m "[tcpctrl] add reply codes and formatter"
```

---

## Task 4: TCP Protocol Helpers

**Files:**
- Create: `internal/tcpctrl/protocol.go`

**Interfaces:**
- Consumes: `FormatReply` from Task 3 (same package)
- Produces: `ReadLine(conn net.Conn) (string, error)`, `WriteReply(conn net.Conn, code int, msg string) error`

- [ ] **Step 1: Write protocol.go**

Create `internal/tcpctrl/protocol.go`:

```go
package tcpctrl

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
)

func ReadLine(conn net.Conn) (string, error) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read line: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	return line, nil
}

func WriteReply(conn net.Conn, code int, msg string) error {
	reply := FormatReply(code, msg)
	_, err := io.WriteString(conn, reply)
	if err != nil {
		return fmt.Errorf("write reply: %w", err)
	}
	return nil
}
```

Note: Unit tests for this file are covered by integration tests (Task 15) since it wraps `net.Conn`.

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/tcpctrl/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/tcpctrl/protocol.go
git commit -m "[tcpctrl] add TCP read/write helpers"
```

---

## Task 5: Authentication

**Files:**
- Create: `internal/auth/auth.go`
- Create: `internal/auth/auth_test.go`

**Interfaces:**
- Consumes: `users.conf` from Task 1
- Produces: `CheckPassword(username, password string) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/auth_test.go`:

```go
package auth

import (
	"os"
	"testing"
)

const testUsersConf = `# test credentials
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
`

func setupTest(t *testing.T, confContent string) string {
	t.Helper()
	f, err := os.CreateTemp("", "users.conf.*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(confContent); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestCheckPassword_ValidLogin(t *testing.T) {
	path := setupTest(t, testUsersConf)
	defer os.Remove(path)

	if !CheckPassword(path, "admin", "admin") {
		t.Error("CheckPassword(admin, admin) should return true")
	}
	if !CheckPassword(path, "user1", "user") {
		t.Error("CheckPassword(user1, user) should return true")
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	path := setupTest(t, testUsersConf)
	defer os.Remove(path)

	if CheckPassword(path, "admin", "wrongpassword") {
		t.Error("CheckPassword(admin, wrongpassword) should return false")
	}
}

func TestCheckPassword_NonexistentUser(t *testing.T) {
	path := setupTest(t, testUsersConf)
	defer os.Remove(path)

	if CheckPassword(path, "nonexistent", "anything") {
		t.Error("CheckPassword(nonexistent, anything) should return false")
	}
}

func TestCheckPassword_EmptyFile(t *testing.T) {
	path := setupTest(t, "")
	defer os.Remove(path)

	if CheckPassword(path, "admin", "admin") {
		t.Error("CheckPassword on empty file should return false")
	}
}

func TestCheckPassword_Comments(t *testing.T) {
	conf := `# this is a comment

# another comment
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
`
	path := setupTest(t, conf)
	defer os.Remove(path)

	if !CheckPassword(path, "admin", "admin") {
		t.Error("CheckPassword should ignore comments and blanks")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/ -v`
Expected: compilation error (undefined: CheckPassword)

- [ ] **Step 3: Write minimal implementation**

Create `internal/auth/auth.go`:

```go
package auth

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"strings"
)

func CheckPassword(usersFile, username, password string) bool {
	f, err := os.Open(usersFile)
	if err != nil {
		log.Printf("[AUTH] cannot open users file %s: %v", usersFile, err)
		return false
	}
	defer f.Close()

	hash := sha256.Sum256([]byte(password))
	expectedHash := fmt.Sprintf("%x", hash)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == username {
			match := strings.EqualFold(parts[1], expectedHash)
			if match {
				log.Printf("[AUTH] user %s authenticated", username)
			} else {
				log.Printf("[AUTH] user %s password mismatch", username)
			}
			return match
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[AUTH] error reading users file: %v", err)
	}
	log.Printf("[AUTH] user %s not found", username)
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/ -v`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/auth.go internal/auth/auth_test.go
git commit -m "[auth] add SHA-256 password authentication"
```

---

## Task 6: Session Manager

**Files:**
- Create: `internal/session/manager.go`
- Create: `internal/session/manager_test.go`

**Interfaces:**
- Produces: `type Session struct`, `type Manager struct`, `NewManager() *Manager`, `(*Manager).Register(conn net.Conn) *Session`, `(*Manager).Unregister(id uint32)`, `(*Manager).Get(id uint32) (*Session, bool)`

- [ ] **Step 1: Write the failing test**

Create `internal/session/manager_test.go`:

```go
package session

import (
	"net"
	"sync"
	"testing"
)

type fakeConn struct{ net.Conn }

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager() should not return nil")
	}
}

func TestRegister(t *testing.T) {
	m := NewManager()
	conn1, _ := net.Pipe()
	defer conn1.Close()

	s := m.Register(conn1)
	s2 := m.Register(conn1)

	if s.ID == s2.ID {
		t.Error("register should assign unique IDs")
	}
	if s.ID == 0 || s2.ID == 0 {
		t.Error("register should assign non-zero IDs")
	}
}

func TestGet(t *testing.T) {
	m := NewManager()
	conn, _ := net.Pipe()
	defer conn.Close()

	s := m.Register(conn)
	got, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("Get should find registered session")
	}
	if got.ID != s.ID {
		t.Errorf("Get returned session ID %d, want %d", got.ID, s.ID)
	}
}

func TestGet_Missing(t *testing.T) {
	m := NewManager()
	_, ok := m.Get(9999)
	if ok {
		t.Error("Get for unregistered ID should return false")
	}
}

func TestUnregister(t *testing.T) {
	m := NewManager()
	conn, _ := net.Pipe()
	defer conn.Close()

	s := m.Register(conn)
	m.Unregister(s.ID)

	_, ok := m.Get(s.ID)
	if ok {
		t.Error("Get should return false after Unregister")
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			conn, _ := net.Pipe()
			defer conn.Close()
			s := m.Register(conn)
			got, ok := m.Get(s.ID)
			if !ok || got.ID != s.ID {
				t.Errorf("concurrent access failed for iteration %d", n)
			}
			m.Unregister(s.ID)
		}(i)
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -v`
Expected: compilation error (undefined: Manager, Session, etc.)

- [ ] **Step 3: Write minimal implementation**

Create `internal/session/manager.go`:

```go
package session

import (
	"context"
	"log"
	"net"
	"sync"
)

type Session struct {
	ID            uint32
	Conn          net.Conn
	Username      string
	Authenticated bool
	CWD           string
	DataConn      *net.UDPConn
	TransferType  byte
	Ctx           context.Context
	Cancel        context.CancelFunc
}

type Manager struct {
	mu       sync.RWMutex
	registry map[uint32]*Session
	nextID   uint32
}

func NewManager() *Manager {
	return &Manager{
		registry: make(map[uint32]*Session),
		nextID:   1,
	}
}

func (m *Manager) Register(conn net.Conn) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextID
	m.nextID++

	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:     id,
		Conn:   conn,
		CWD:    "/",
		TransferType: 'A',
		Ctx:    ctx,
		Cancel: cancel,
	}
	m.registry[id] = s

	log.Printf("[SESS] registered session %d", id)
	return s
}

func (m *Manager) Unregister(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.registry[id]; ok {
		s.Cancel()
		if s.DataConn != nil {
			s.DataConn.Close()
		}
		delete(m.registry, id)
		log.Printf("[SESS] unregistered session %d", id)
	}
}

func (m *Manager) Get(id uint32) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.registry[id]
	return s, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -v`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/session/manager.go internal/session/manager_test.go
git commit -m "[session] add session struct and manager"
```

---

## Task 7: RDT Packet Header

**Files:**
- Create: `internal/rdt/header.go`
- Create: `internal/rdt/header_test.go`

**Interfaces:**
- Produces: `type Header struct`, flag constants (`FlagSYN`, `FlagFIN`, `FlagACK`, `FlagRST`, `FlagDATA`), `(*Header).Encode(payload []byte) ([]byte, error)`, `Decode(data []byte) (*Header, []byte, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/rdt/header_test.go`:

```go
package rdt

import (
	"testing"
)

func TestHeaderEncodeDecode_RoundTrip(t *testing.T) {
	payload := []byte("hello world payload data")
	h := &Header{
		SessionID:  42,
		SeqNum:     1234,
		AckNum:     5678,
		Flags:      FlagDATA | FlagACK,
		PayloadLen: uint16(len(payload)),
		Window:     64000,
		Checksum:   0,
	}

	encoded, err := h.Encode(payload)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	expectedLen := HeaderSize + len(payload)
	if len(encoded) != expectedLen {
		t.Errorf("encoded length = %d, want %d", len(encoded), expectedLen)
	}

	decoded, decodedPayload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if decoded.SessionID != h.SessionID {
		t.Errorf("SessionID = %d, want %d", decoded.SessionID, h.SessionID)
	}
	if decoded.SeqNum != h.SeqNum {
		t.Errorf("SeqNum = %d, want %d", decoded.SeqNum, h.SeqNum)
	}
	if decoded.AckNum != h.AckNum {
		t.Errorf("AckNum = %d, want %d", decoded.AckNum, h.AckNum)
	}
	if decoded.Flags != h.Flags {
		t.Errorf("Flags = %02x, want %02x", decoded.Flags, h.Flags)
	}
	if decoded.PayloadLen != h.PayloadLen {
		t.Errorf("PayloadLen = %d, want %d", decoded.PayloadLen, h.PayloadLen)
	}
	if decoded.Window != h.Window {
		t.Errorf("Window = %d, want %d", decoded.Window, h.Window)
	}
	if string(decodedPayload) != string(payload) {
		t.Errorf("payload mismatch: %q vs %q", decodedPayload, payload)
	}
}

func TestHeaderEncodeDecode_NoPayload(t *testing.T) {
	h := &Header{
		SessionID:  1,
		SeqNum:     0,
		AckNum:     100,
		Flags:      FlagACK,
		PayloadLen: 0,
		Window:     65535,
	}

	encoded, err := h.Encode(nil)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(encoded) != HeaderSize {
		t.Errorf("encoded length = %d, want %d", len(encoded), HeaderSize)
	}

	decoded, payload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if payload != nil {
		t.Errorf("payload should be nil for zero-length data")
	}
	if decoded.Flags != FlagACK {
		t.Errorf("Flags = %02x, want %02x", decoded.Flags, FlagACK)
	}
}

func TestHeaderDecode_TooShort(t *testing.T) {
	short := make([]byte, HeaderSize-1)
	_, _, err := Decode(short)
	if err == nil {
		t.Error("Decode should fail on short buffer")
	}
}

func TestHeaderDecode_PayloadLengthMismatch(t *testing.T) {
	h := &Header{
		SessionID:  1,
		PayloadLen: 100,
	}
	encoded, _ := h.Encode(nil) // no payload but claims 100
	encoded = append(encoded, make([]byte, 50)...) // actually 50 bytes
	_, _, err := Decode(encoded)
	if err == nil {
		t.Error("Decode should fail on payload length mismatch")
	}
}

func TestHeaderDecode_LargePayload(t *testing.T) {
	payload := make([]byte, 1400)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	h := &Header{
		SessionID:  999,
		SeqNum:     1400,
		Flags:      FlagDATA,
		PayloadLen: 1400,
		Window:     32000,
	}
	encoded, _ := h.Encode(payload)
	decoded, decPayload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if decoded.PayloadLen != 1400 {
		t.Errorf("PayloadLen = %d, want 1400", decoded.PayloadLen)
	}
	if len(decPayload) != 1400 {
		t.Errorf("decoded payload length = %d, want 1400", len(decPayload))
	}
}

func TestFlags_Const(t *testing.T) {
	if FlagSYN != 0x01 {
		t.Errorf("FlagSYN = %02x, want 0x01", FlagSYN)
	}
	if FlagFIN != 0x02 {
		t.Errorf("FlagFIN = %02x, want 0x02", FlagFIN)
	}
	if FlagACK != 0x04 {
		t.Errorf("FlagACK = %02x, want 0x04", FlagACK)
	}
	if FlagRST != 0x08 {
		t.Errorf("FlagRST = %02x, want 0x08", FlagRST)
	}
	if FlagDATA != 0x10 {
		t.Errorf("FlagDATA = %02x, want 0x10", FlagDATA)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rdt/ -v -run TestHeader`
Expected: compilation error (undefined: Header, HeaderSize, FlagSYN, etc.)

- [ ] **Step 3: Write minimal implementation**

Create `internal/rdt/header.go`:

```go
package rdt

import (
	"encoding/binary"
	"fmt"
)

const HeaderSize = 21

const (
	FlagSYN  uint8 = 0x01
	FlagFIN        = 0x02
	FlagACK        = 0x04
	FlagRST        = 0x08
	FlagDATA       = 0x10
)

type Header struct {
	SessionID  uint32
	SeqNum     uint32
	AckNum     uint32
	Flags      uint8
	PayloadLen uint16
	Window     uint16
	Checksum   uint32
}

func (h *Header) Encode(payload []byte) ([]byte, error) {
	h.PayloadLen = uint16(len(payload))
	buf := make([]byte, HeaderSize+len(payload))

	binary.BigEndian.PutUint32(buf[0:4], h.SessionID)
	binary.BigEndian.PutUint32(buf[4:8], h.SeqNum)
	binary.BigEndian.PutUint32(buf[8:12], h.AckNum)
	buf[12] = h.Flags
	binary.BigEndian.PutUint16(buf[13:15], h.PayloadLen)
	binary.BigEndian.PutUint16(buf[15:17], h.Window)
	binary.BigEndian.PutUint32(buf[17:21], h.Checksum)

	copy(buf[HeaderSize:], payload)
	return buf, nil
}

func Decode(data []byte) (*Header, []byte, error) {
	if len(data) < HeaderSize {
		return nil, nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}
	h := &Header{
		SessionID:  binary.BigEndian.Uint32(data[0:4]),
		SeqNum:     binary.BigEndian.Uint32(data[4:8]),
		AckNum:     binary.BigEndian.Uint32(data[8:12]),
		Flags:      data[12],
		PayloadLen: binary.BigEndian.Uint16(data[13:15]),
		Window:     binary.BigEndian.Uint16(data[15:17]),
		Checksum:   binary.BigEndian.Uint32(data[17:21]),
	}

	expectedTotal := HeaderSize + int(h.PayloadLen)
	if len(data) < expectedTotal {
		return nil, nil, fmt.Errorf("expected %d bytes for payload, got %d", expectedTotal, len(data))
	}

	payload := data[HeaderSize:expectedTotal]
	return h, payload, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rdt/ -v -run TestHeader`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rdt/header.go internal/rdt/header_test.go
git commit -m "[rdt] add 21-byte packet header encode/decode"
```

---

## Task 8: CRC32 Checksum

**Files:**
- Create: `internal/rdt/checksum.go`
- Create: `internal/rdt/checksum_test.go`

**Interfaces:**
- Consumes: `Header` from Task 7 (same package), `HeaderSize` constant
- Produces: `ComputeChecksum(data []byte) uint32`, `VerifyChecksum(data []byte) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/rdt/checksum_test.go`:

```go
package rdt

import (
	"testing"
)

func TestComputeAndVerify_Success(t *testing.T) {
	h := &Header{
		SessionID: 1,
		SeqNum:    100,
		AckNum:    200,
		Flags:     FlagDATA,
		Window:    65535,
	}
	payload := []byte("test payload for checksum verification")
	encoded, _ := h.Encode(payload)

	originalChecksum := binary.BigEndian.Uint32(encoded[17:21])
	t.Logf("original checksum field (before compute): %08x", originalChecksum)

	checksum := ComputeChecksum(encoded)
	t.Logf("computed checksum: %08x", checksum)

	binary.BigEndian.PutUint32(encoded[17:21], checksum)

	if !VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return true for valid packet")
	}
}

func TestVerifyChecksum_Corruption(t *testing.T) {
	h := &Header{
		SessionID: 1,
		SeqNum:    100,
	}
	payload := []byte("data")
	encoded, _ := h.Encode(payload)

	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)

	encoded[HeaderSize] ^= 0xFF

	if VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return false for corrupted packet")
	}
}

func TestComputeChecksum_Idempotent(t *testing.T) {
	h := &Header{SessionID: 42}
	payload := []byte("consistent")
	encoded, _ := h.Encode(payload)

	c1 := ComputeChecksum(encoded)
	c2 := ComputeChecksum(encoded)
	if c1 != c2 {
		t.Error("ComputeChecksum should produce same result for same input")
	}
}

func TestVerifyChecksum_ZeroChecksum(t *testing.T) {
	h := &Header{SessionID: 1}
	payload := []byte("x")
	encoded, _ := h.Encode(payload)
	binary.BigEndian.PutUint32(encoded[17:21], 0)

	if VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return false when checksum field is zero (not yet set)")
	}
}

func TestVerifyChecksum_EmptyPacket(t *testing.T) {
	h := &Header{
		SessionID:  5,
		SeqNum:     0,
		Flags:      FlagACK,
		PayloadLen: 0,
		Window:     65535,
	}
	encoded, _ := h.Encode(nil)
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)

	if !VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return true for valid ACK-only packet")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rdt/ -v -run TestComputeAndVerify`
Expected: compilation error (undefined: ComputeChecksum, VerifyChecksum)

- [ ] **Step 3: Write minimal implementation**

Create `internal/rdt/checksum.go`:

```go
package rdt

import (
	"encoding/binary"
	"hash/crc32"
)

var ieeeTable = crc32.MakeTable(crc32.IEEE)

func ComputeChecksum(data []byte) uint32 {
	if len(data) < HeaderSize {
		return 0
	}
	packet := make([]byte, len(data))
	copy(packet, data)
	binary.BigEndian.PutUint32(packet[17:21], 0)
	return crc32.Checksum(packet, ieeeTable)
}

func VerifyChecksum(data []byte) bool {
	if len(data) < HeaderSize {
		return false
	}
	computed := ComputeChecksum(data)
	stored := binary.BigEndian.Uint32(data[17:21])
	return computed == stored
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rdt/ -v -run TestComputeAndVerify`
Expected: all checksum tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rdt/checksum.go internal/rdt/checksum_test.go
git commit -m "[rdt] add CRC32-IEEE checksum computation"
```

---

## Task 9: Per-Packet Timers

**Files:**
- Create: `internal/rdt/timer.go`
- Create: `internal/rdt/timer_test.go`

**Interfaces:**
- Produces: `type TimerManager struct`, `NewTimerManager(baseTimeout time.Duration, callback func(seq uint32)) *TimerManager`, `(*TimerManager).Start(seq uint32)`, `(*TimerManager).Cancel(seq uint32)`, `(*TimerManager).StopAll()`

- [ ] **Step 1: Write the failing test**

Create `internal/rdt/timer_test.go`:

```go
package rdt

import (
	"sync"
	"testing"
	"time"
)

func TestTimerStartAndCancel(t *testing.T) {
	expired := make(chan uint32, 10)
	tm := NewTimerManager(200*time.Millisecond, func(seq uint32) {
		expired <- seq
	})
	defer tm.StopAll()

	tm.Start(1)
	tm.Start(2)
	tm.Cancel(1)

	select {
	case seq := <-expired:
		if seq != 2 {
			t.Errorf("expected seq 2 to expire, got %d", seq)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timer for seq 2 should have expired")
	}

	select {
	case <-expired:
		t.Error("timer for seq 1 should have been cancelled")
	case <-time.After(300 * time.Millisecond):
		// expected: no expiry for seq 1
	}
}

func TestTimerBackoff(t *testing.T) {
	var mu sync.Mutex
	expiries := make([]uint32, 0)
	tm := NewTimerManager(100*time.Millisecond, func(seq uint32) {
		mu.Lock()
		expiries = append(expiries, seq)
		mu.Unlock()
	})
	defer tm.StopAll()

	tm.Start(5)
	time.Sleep(200 * time.Millisecond)

	tm.Start(5)
	time.Sleep(250 * time.Millisecond)

	tm.Start(5)
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	count := len(expiries)
	mu.Unlock()
	t.Logf("timer for seq 5 expired %d times", count)
	if count < 2 {
		t.Errorf("expected at least 2 expiries, got %d", count)
	}
}

func TestTimerStopAll(t *testing.T) {
	expired := make(chan uint32, 10)
	tm := NewTimerManager(100*time.Millisecond, func(seq uint32) {
		expired <- seq
	})

	tm.Start(1)
	tm.Start(2)
	tm.Start(3)
	tm.StopAll()

	select {
	case seq := <-expired:
		t.Errorf("no timer should expire after StopAll, got seq %d", seq)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rdt/ -v -run TestTimer`
Expected: compilation error (undefined: TimerManager, NewTimerManager)

- [ ] **Step 3: Write minimal implementation**

Create `internal/rdt/timer.go`:

```go
package rdt

import (
	"sync"
	"time"
)

const (
	initialTimeout = 500 * time.Millisecond
	maxTimeout     = 60 * time.Second
	maxRetransmits = 3
)

type TimerManager struct {
	mu       sync.Mutex
	timers   map[uint32]*time.Timer
	backoff  map[uint32]int
	timeout  time.Duration
	onExpire func(seq uint32)
}

func NewTimerManager(baseTimeout time.Duration, callback func(seq uint32)) *TimerManager {
	if baseTimeout <= 0 {
		baseTimeout = initialTimeout
	}
	return &TimerManager{
		timers:   make(map[uint32]*time.Timer),
		backoff:  make(map[uint32]int),
		timeout:  baseTimeout,
		onExpire: callback,
	}
}

func (tm *TimerManager) Start(seq uint32) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if t, ok := tm.timers[seq]; ok {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}

	retries := tm.backoff[seq]
	delay := tm.timeout
	for i := 0; i < retries; i++ {
		delay *= 2
	}
	if delay > maxTimeout {
		delay = maxTimeout
	}

	tm.timers[seq] = time.AfterFunc(delay, func() {
		tm.mu.Lock()
		tm.backoff[seq]++
		tm.mu.Unlock()
		tm.onExpire(seq)
	})
}

func (tm *TimerManager) Cancel(seq uint32) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if t, ok := tm.timers[seq]; ok {
		t.Stop()
		delete(tm.timers, seq)
	}
	delete(tm.backoff, seq)
}

func (tm *TimerManager) StopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, t := range tm.timers {
		t.Stop()
	}
	tm.timers = make(map[uint32]*time.Timer)
	tm.backoff = make(map[uint32]int)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rdt/ -v -run TestTimer`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rdt/timer.go internal/rdt/timer_test.go
git commit -m "[rdt] add per-packet retransmit timers"
```

---

## Task 10: RDT Receiver (Selective Repeat)

**Files:**
- Create: `internal/rdt/receiver.go`

**Interfaces:**
- Consumes: `Header`, `HeaderSize`, flag constants from Task 7, `ComputeChecksum`, `VerifyChecksum` from Task 8
- Produces: `type Receiver struct`, `NewReceiver(conn *net.UDPConn, sessionID uint32) *Receiver`, `(*Receiver).Accept() error`, `(*Receiver).Connect(addr *net.UDPAddr) error`, `(*Receiver).Receive() <-chan []byte`, `(*Receiver).Close() error`

**Design notes:**
- States: closed, handshaking (SYN→SYN+ACK→ACK), established (receiving DATA), finWaiting (rcv FIN, send FIN+ACK)
- In-order packets delivered to `output` channel; out-of-order buffered in `recvBuffer` map
- ACK sent for every received DATA packet — piggybacked ACK on DATA, or standalone ACK otherwise
- Window field advertises remaining buffer: `65535 - len(recvBuffer) * 1400`
- Sequence numbers are byte-oriented; rcvBase is the next expected byte offset

- [ ] **Step 1: Write receiver.go**

Create `internal/rdt/receiver.go`:

```go
package rdt

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

type receiverState int

const (
	recvClosed receiverState = iota
	recvAccepting
	recvEstablished
	recvFinWaiting
)

type receivedPacket struct {
	seq  uint32
	data []byte
}

type Receiver struct {
	conn       *net.UDPConn
	sessionID  uint32
	remoteAddr *net.UDPAddr
	mu         sync.Mutex
	state      receiverState
	rcvBase    uint32
	recvBuffer map[uint32][]byte
	recvWindow uint16
	maxBuffer  int
	output     chan []byte
	done       chan struct{}
	lastAck    chan struct{}
}

func NewReceiver(conn *net.UDPConn, sessionID uint32) *Receiver {
	return &Receiver{
		conn:       conn,
		sessionID:  sessionID,
		state:      recvClosed,
		recvBuffer: make(map[uint32][]byte),
		recvWindow: 65535,
		maxBuffer:  1000,
		output:     make(chan []byte, 100),
		done:       make(chan struct{}),
	}
}

func (r *Receiver) Accept() error {
	r.mu.Lock()
	r.state = recvAccepting
	r.mu.Unlock()

	for {
		buf := make([]byte, 1500)
		n, addr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("accept read: %w", err)
		}

		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}

		if hdr.SessionID != r.sessionID || hdr.Flags != FlagSYN {
			continue
		}

		r.mu.Lock()
		r.remoteAddr = addr
		r.rcvBase = hdr.SeqNum + 1
		myISN := uint32(rand.Int31())
		r.sendControl(myISN, r.rcvBase, FlagSYN|FlagACK)

		r.mu.Unlock()

		for {
			buf := make([]byte, 1500)
			n, _, err := r.conn.ReadFromUDP(buf)
			if err != nil {
				return fmt.Errorf("third ack read: %w", err)
			}
			hdr, _, err := Decode(buf[:n])
			if err != nil {
				continue
			}
			if !VerifyChecksum(buf[:n]) {
				continue
			}
			if hdr.SessionID != r.sessionID || hdr.Flags != FlagACK {
				continue
			}
			break
		}

		r.mu.Lock()
		r.state = recvEstablished
		r.mu.Unlock()

		go r.receiveLoop()
		return nil
	}
}

func (r *Receiver) Connect(addr *net.UDPAddr) error {
	r.mu.Lock()
	r.remoteAddr = addr
	r.mu.Unlock()

	myISN := uint32(rand.Int31())
	if err := r.sendControl(myISN, 0, FlagSYN); err != nil {
		return fmt.Errorf("connect syn: %w", err)
	}

	for {
		buf := make([]byte, 1500)
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("connect read synack: %w", err)
		}
		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}
		if hdr.SessionID != r.sessionID || hdr.Flags != (FlagSYN|FlagACK) {
			continue
		}

		r.mu.Lock()
		r.rcvBase = hdr.SeqNum + 1
		if err := r.sendControl(0, r.rcvBase, FlagACK); err != nil {
			r.mu.Unlock()
			return fmt.Errorf("connect ack: %w", err)
		}
		r.state = recvEstablished
		r.mu.Unlock()

		go r.receiveLoop()
		return nil
	}
}

func (r *Receiver) Receive() <-chan []byte {
	return r.output
}

func (r *Receiver) Close() error {
	r.mu.Lock()
	close(r.done)
	r.mu.Unlock()

	if r.state == recvFinWaiting {
		if err := r.sendControl(0, r.rcvBase, FlagFIN|FlagACK); err != nil {
			return fmt.Errorf("close finack: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	close(r.output)
	return nil
}

func (r *Receiver) sendControl(seqNum, ackNum uint32, flags uint8) error {
	h := &Header{
		SessionID:  r.sessionID,
		SeqNum:     seqNum,
		AckNum:     ackNum,
		Flags:      flags,
		PayloadLen: 0,
		Window:     r.recvWindow,
	}
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode control: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)

	_, err = r.conn.WriteToUDP(encoded, r.remoteAddr)
	if err != nil {
		return fmt.Errorf("send control: %w", err)
	}
	return nil
}

func (r *Receiver) sendAck(ackNum uint32) {
	h := &Header{
		SessionID:  r.sessionID,
		SeqNum:     0,
		AckNum:     ackNum,
		Flags:      FlagACK,
		PayloadLen: 0,
		Window:     r.windowAvailable(),
	}
	encoded, _ := h.Encode(nil)
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)
	r.conn.WriteToUDP(encoded, r.remoteAddr)
}

func (r *Receiver) windowAvailable() uint16 {
	buffered := uint16(len(r.recvBuffer) * 1400)
	if buffered > r.recvWindow {
		return 0
	}
	return r.recvWindow - buffered
}

func (r *Receiver) receiveLoop() {
	log.Printf("[RDT] receiver loop started, session=%d", r.sessionID)
	for {
		select {
		case <-r.done:
			return
		default:
		}

		r.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("[RDT] receiver read error: %v", err)
			return
		}

		if !VerifyChecksum(buf[:n]) {
			log.Printf("[RDT] receiver checksum fail")
			continue
		}

		hdr, payload, err := Decode(buf[:n])
		if err != nil {
			log.Printf("[RDT] receiver decode error: %v", err)
			continue
		}

		if hdr.SessionID != r.sessionID {
			continue
		}

		r.mu.Lock()

		switch {
		case hdr.Flags&FlagRST != 0:
			log.Printf("[RDT] receiver got RST, closing")
			r.state = recvClosed
			r.mu.Unlock()
			close(r.done)
			return

		case hdr.Flags&FlagFIN != 0:
			log.Printf("[RDT] receiver got FIN")
			r.state = recvFinWaiting
			r.mu.Unlock()
			return

		case hdr.Flags&FlagDATA != 0:
			r.handleData(hdr, payload)
			r.mu.Unlock()

		default:
			r.mu.Unlock()
		}
	}
}

func (r *Receiver) handleData(hdr *Header, payload []byte) {
	if hdr.SeqNum < r.rcvBase {
		r.sendAck(r.rcvBase)
		return
	}

	dataLen := uint32(len(payload))
	if hdr.SeqNum == r.rcvBase {
		r.output <- payload
		r.rcvBase += dataLen

		for {
			buffered, ok := r.recvBuffer[r.rcvBase]
			if !ok {
				break
			}
			r.output <- buffered
			r.rcvBase += uint32(len(buffered))
			delete(r.recvBuffer, r.rcvBase-uint32(len(buffered)))
		}

		r.sendAck(r.rcvBase)
	} else if hdr.SeqNum > r.rcvBase {
		if _, exists := r.recvBuffer[hdr.SeqNum]; !exists {
			if len(r.recvBuffer) < r.maxBuffer {
				r.recvBuffer[hdr.SeqNum] = payload
				log.Printf("[RDT] buffered out-of-order seq=%d (rcvBase=%d)", hdr.SeqNum, r.rcvBase)
			}
		}
		r.sendAck(r.rcvBase)
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/rdt/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/rdt/receiver.go
git commit -m "[rdt] add RDT receiver with Selective Repeat"
```

---

## Task 11: RDT Sender (Selective Repeat)

**Files:**
- Create: `internal/rdt/sender.go`

**Interfaces:**
- Consumes: `Header`, `HeaderSize`, flag constants from Task 7, `ComputeChecksum`, `VerifyChecksum` from Task 8, `TimerManager` from Task 9
- Produces: `type Sender struct`, `NewSender(conn *net.UDPConn, sessionID uint32) *Sender`, `(*Sender).Connect(addr *net.UDPAddr) error`, `(*Sender).Accept() error`, `(*Sender).Send(data []byte) error`, `(*Sender).Close() error`

**Design notes:**
- Fixed cwnd = 28000 bytes (20 * 1400)
- Effective window = min(28000, recvWindow)
- Block on Send when inflight >= effective window
- Each packet in inflight has its own timer via TimerManager
- On ACK: cancel timer, advance sendBase
- On timer expiry: retransmit packet, double timeout (handled by TimerManager)
- After 3 consecutive timeouts for same packet (internal to TimerManager, tracked via retry count): send RST, return error
- Sequence numbers are byte-oriented

- [ ] **Step 1: Write sender.go**

Create `internal/rdt/sender.go`:

```go
package rdt

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	fixedCWND  = 28000
)

type senderState int

const (
	sendClosed senderState = iota
	sendSynSent
	sendEstablished
	sendFinSent
)

type packetRecord struct {
	data      []byte
	sentAt    time.Time
	retransCount int
	seq       uint32
	length    uint32
	done      bool
}

type Sender struct {
	conn       *net.UDPConn
	sessionID  uint32
	remoteAddr *net.UDPAddr
	mu         sync.Mutex
	state      senderState
	sendBase   uint32
	nextSeqNum uint32
	windowSize uint32
	recvWindow uint16
	inflight   map[uint32]*packetRecord
	timers     *TimerManager
	outbox     chan []byte
	ackCh      chan ackEvent
	done       chan struct{}
	errCh      chan error
}

type ackEvent struct {
	ackNum uint32
	window uint16
}

func NewSender(conn *net.UDPConn, sessionID uint32) *Sender {
	s := &Sender{
		conn:       conn,
		sessionID:  sessionID,
		state:      sendClosed,
		windowSize: fixedCWND,
		recvWindow: 65535,
		inflight:   make(map[uint32]*packetRecord),
		outbox:     make(chan []byte, 100),
		ackCh:      make(chan ackEvent, 100),
		done:       make(chan struct{}),
		errCh:      make(chan error, 1),
	}
	s.timers = NewTimerManager(500*time.Millisecond, s.onTimeout)
	return s
}

func (s *Sender) Connect(addr *net.UDPAddr) error {
	s.mu.Lock()
	s.remoteAddr = addr
	s.mu.Unlock()

	myISN := uint32(rand.Int31())
	if err := s.sendControl(myISN, 0, FlagSYN); err != nil {
		return fmt.Errorf("connect syn: %w", err)
	}

	s.mu.Lock()
	s.state = sendSynSent
	s.mu.Unlock()

	for {
		buf := make([]byte, 1500)
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("connect read: %w", err)
		}
		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}
		if hdr.SessionID != s.sessionID || hdr.Flags != (FlagSYN|FlagACK) {
			continue
		}

		s.mu.Lock()
		s.recvWindow = hdr.Window
		s.sendBase = myISN + 1
		s.nextSeqNum = myISN + 1
		if err := s.sendControl(0, hdr.SeqNum+1, FlagACK); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("connect ack: %w", err)
		}
		s.state = sendEstablished
		s.mu.Unlock()

		go s.sendLoop()
		go s.recvLoop()
		return nil
	}
}

func (s *Sender) Accept() error {
	for {
		buf := make([]byte, 1500)
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("accept read: %w", err)
		}

		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}
		if hdr.SessionID != s.sessionID || hdr.Flags != FlagSYN {
			continue
		}

		s.mu.Lock()
		s.remoteAddr = addr
		s.recvWindow = hdr.Window
		myISN := uint32(rand.Int31())
		s.sendBase = myISN + 1
		s.nextSeqNum = myISN + 1
		s.sendControl(myISN, hdr.SeqNum+1, FlagSYN|FlagACK)
		s.mu.Unlock()

		for {
			buf := make([]byte, 1500)
			n, _, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				return fmt.Errorf("accept third ack: %w", err)
			}
			hdr, _, err := Decode(buf[:n])
			if err != nil {
				continue
			}
			if !VerifyChecksum(buf[:n]) {
				continue
			}
			if hdr.SessionID != s.sessionID || hdr.Flags != FlagACK {
				continue
			}
			break
		}

		s.mu.Lock()
		s.state = sendEstablished
		s.mu.Unlock()

		go s.sendLoop()
		go s.recvLoop()
		return nil
	}
}

func (s *Sender) Send(data []byte) error {
	select {
	case s.outbox <- data:
		return nil
	case <-s.done:
		return fmt.Errorf("sender closed")
	case <-s.errCh:
		err := <-s.errCh
		return err
	}
}

func (s *Sender) Close() error {
	s.mu.Lock()
	if s.state != sendEstablished {
		s.mu.Unlock()
		return fmt.Errorf("sender not established")
	}
	s.state = sendFinSent
	s.mu.Unlock()

	time.Sleep(100 * time.Millisecond)

	if err := s.sendControl(0, 0, FlagFIN); err != nil {
		return fmt.Errorf("close fin: %w", err)
	}

	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func (s *Sender) sendControl(seqNum, ackNum uint32, flags uint8) error {
	h := &Header{
		SessionID:  s.sessionID,
		SeqNum:     seqNum,
		AckNum:     ackNum,
		Flags:      flags,
		PayloadLen: 0,
		Window:     65535,
	}
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode control: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)
	_, err = s.conn.WriteToUDP(encoded, s.remoteAddr)
	if err != nil {
		return fmt.Errorf("send control: %w", err)
	}
	return nil
}

func (s *Sender) effectiveWindow() uint32 {
	w := s.windowSize
	if uint32(s.recvWindow) < w {
		w = uint32(s.recvWindow)
	}
	return w
}

func (s *Sender) inflightBytes() uint32 {
	var total uint32
	for _, r := range s.inflight {
		total += r.length
	}
	return total
}

func (s *Sender) sendLoop() {
	log.Printf("[RDT] sender loop started, session=%d", s.sessionID)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		var data []byte
		select {
		case data = <-s.outbox:
		case <-s.done:
			return
		case <-s.errCh:
			return
		}

		offset := 0
		for offset < len(data) {
			s.mu.Lock()
			for s.inflightBytes() >= s.effectiveWindow() {
				s.mu.Unlock()
				select {
				case <-s.ackCh:
				case <-s.done:
					return
				case <-time.After(10 * time.Millisecond):
				}
				s.mu.Lock()
			}

			end := offset + 1400
			if end > len(data) {
				end = len(data)
			}
			chunk := data[offset:end]
			chunkLen := uint32(len(chunk))

			seq := s.nextSeqNum
			s.nextSeqNum += chunkLen

			h := &Header{
				SessionID:  s.sessionID,
				SeqNum:     seq,
				AckNum:     0,
				Flags:      FlagDATA,
				PayloadLen: uint16(chunkLen),
				Window:     65535,
			}
			encoded, _ := h.Encode(chunk)
			checksum := ComputeChecksum(encoded)
			binary.BigEndian.PutUint32(encoded[17:21], checksum)

			if _, err := s.conn.WriteToUDP(encoded, s.remoteAddr); err != nil {
				log.Printf("[RDT] sender write error: %v", err)
				s.mu.Unlock()
				return
			}

			s.inflight[seq] = &packetRecord{
				data:   encoded,
				seq:    seq,
				length: chunkLen,
				sentAt: time.Now(),
			}
			s.timers.Start(seq)
			s.mu.Unlock()

			offset = end
		}
	}
}

func (s *Sender) recvLoop() {
	log.Printf("[RDT] sender recv loop started, session=%d", s.sessionID)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("[RDT] sender recv error: %v", err)
			return
		}

		if !VerifyChecksum(buf[:n]) {
			continue
		}

		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}

		if hdr.SessionID != s.sessionID {
			continue
		}

		s.mu.Lock()

		switch {
		case hdr.Flags&FlagRST != 0:
			log.Printf("[RDT] sender got RST")
			s.state = sendClosed
			s.mu.Unlock()
			close(s.done)
			return

		case hdr.Flags&(FlagFIN|FlagACK) == FlagFIN|FlagACK:
			s.state = sendClosed
			s.mu.Unlock()
			close(s.done)
			return

		case hdr.Flags&FlagACK != 0:
			s.recvWindow = hdr.Window
			s.processAck(hdr.AckNum)
			s.mu.Unlock()

		default:
			s.mu.Unlock()
		}
	}
}

func (s *Sender) processAck(ackNum uint32) {
	if ackNum <= s.sendBase {
		return
	}

	for seq := s.sendBase; seq < ackNum; {
		if rec, ok := s.inflight[seq]; ok {
			rec.done = true
			s.timers.Cancel(seq)
			delete(s.inflight, seq)
			seq += rec.length
		} else {
			seq++
		}
	}

	s.sendBase = ackNum
}

func (s *Sender) onTimeout(seq uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.inflight[seq]
	if !ok {
		return
	}

	rec.retransCount++
	if rec.retransCount > maxRetransmits {
		log.Printf("[RDT] max retransmits exceeded for seq=%d", seq)
		s.sendControl(0, 0, FlagRST)
		s.state = sendClosed
		go func() { s.errCh <- fmt.Errorf("max retransmits for seq %d", seq) }()
		close(s.done)
		return
	}

	log.Printf("[RDT] retransmitting seq=%d (attempt %d)", seq, rec.retransCount)
	s.conn.WriteToUDP(rec.data, s.remoteAddr)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/rdt/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/rdt/sender.go
git commit -m "[rdt] add RDT sender with Selective Repeat and fixed cwnd"
```

---

## Task 12: File System Operations

**Files:**
- Create: `internal/fs/ops.go`

**Interfaces:**
- Consumes: `Session` from `internal/session`
- Produces: `ResolvePath(session *session.Session, path string) string`, `ListDir(dirPath string) ([]os.FileInfo, error)`, `OpenForRead(filePath string) (*os.File, os.FileInfo, error)`, `OpenForWrite(filePath string) (*os.File, error)`

- [ ] **Step 1: Write ops.go**

Create `internal/fs/ops.go`:

```go
package fs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"hybrid-ftp/internal/session"
)

func ResolvePath(sess *session.Session, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(sess.CWD, path))
}

func ListDir(dirPath string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dirPath, err)
	}
	infos := make([]os.FileInfo, len(entries))
	for i, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("info %s: %w", entry.Name(), err)
		}
		infos[i] = info
	}
	log.Printf("[FS] listed %s: %d entries", dirPath, len(infos))
	return infos, nil
}

func OpenForRead(filePath string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open for read %s: %w", filePath, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("stat %s: %w", filePath, err)
	}
	if info.IsDir() {
		f.Close()
		return nil, nil, fmt.Errorf("cannot RETR a directory: %s", filePath)
	}
	log.Printf("[FS] opened %s for read (%d bytes)", filePath, info.Size())
	return f, info, nil
}

func OpenForWrite(filePath string) (*os.File, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("open for write %s: %w", filePath, err)
	}
	log.Printf("[FS] opened %s for write", filePath)
	return f, nil
}

func FormatListEntry(info os.FileInfo) string {
	entryType := "-"
	if info.IsDir() {
		entryType = "d"
	}
	mode := info.Mode().String()
	size := info.Size()
	name := info.Name()
	modTime := info.ModTime().Format("Jan 02 15:04")
	return fmt.Sprintf("%s%s %8d %s %s\r\n", entryType, mode[1:], size, modTime, name)
}

func FormatList(infos []os.FileInfo) string {
	var sb strings.Builder
	for _, info := range infos {
		sb.WriteString(FormatListEntry(info))
	}
	return sb.String()
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/fs/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/fs/ops.go
git commit -m "[fs] add file system operations"
```

---

## Task 13: Server Main

**Files:**
- Create: `cmd/server/main.go`

**Interfaces:**
- Consumes: all packages from Tasks 2-12
- Produces: standalone server binary

- [ ] **Step 1: Write server main.go**

Create `cmd/server/main.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"hybrid-ftp/internal/auth"
	"hybrid-ftp/internal/fs"
	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/session"
	"hybrid-ftp/internal/tcpctrl"
)

var (
	ctrlPort  = flag.String("ctrl", ":2121", "TCP control port")
	rootDir   = flag.String("root", "/tmp/hybrid-ftp", "Virtual filesystem root")
	usersFile = flag.String("users", "users.conf", "Path to users.conf")
)

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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("[CTRL] shutting down...")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[CTRL] accept error: %v", err)
			break
		}
		go handleSession(conn, mgr, *rootDir, *usersFile)
	}
}

func handleSession(conn net.Conn, mgr *session.Manager, root, usersPath string) {
	sess := mgr.Register(conn)
	defer mgr.Unregister(sess.ID)
	defer conn.Close()

	log.Printf("[CTRL] session %d connected from %s", sess.ID, conn.RemoteAddr())

	if err := tcpctrl.WriteReply(conn, tcpctrl.ReplyReady, "Hybrid FTP Ready."); err != nil {
		return
	}

	for {
		line, err := tcpctrl.ReadLine(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("[CTRL] session %d read error: %v", sess.ID, err)
			}
			return
		}
		if line == "" {
			continue
		}

		log.Printf("[CTRL] session %d cmd: %s", sess.ID, line)

		cmd, args := tcpctrl.ParseCommand(line)

		if !sess.Authenticated && cmd != tcpctrl.CmdUSER && cmd != tcpctrl.CmdPASS && cmd != tcpctrl.CmdQUIT {
			tcpctrl.WriteReply(conn, tcpctrl.ReplyNotLoggedIn, "Login first.")
			continue
		}

		switch cmd {
		case tcpctrl.CmdUSER:
			sess.Username = args
			tcpctrl.WriteReply(conn, tcpctrl.ReplyNeedPassword, "Password required.")

		case tcpctrl.CmdPASS:
			if sess.Authenticated {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Already logged in.")
				continue
			}
			if auth.CheckPassword(usersPath, sess.Username, args) {
				sess.Authenticated = true
				tcpctrl.WriteReply(conn, tcpctrl.ReplyLoggedIn, "Login successful.")
			} else {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyNotLoggedIn, "Login failed.")
			}

		case tcpctrl.CmdQUIT:
			tcpctrl.WriteReply(conn, tcpctrl.ReplyQuit, "Goodbye.")
			return

		case tcpctrl.CmdPWD:
			absRoot, _ := filepath.Abs(root)
			cwd := filepath.Join(absRoot, sess.CWD)
			tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" is current directory.", cwd))

		case tcpctrl.CmdCWD:
			target := fs.ResolvePath(sess, args)
			absRoot, _ := filepath.Abs(root)
			fullPath := filepath.Join(absRoot, target)
			info, err := os.Stat(fullPath)
			if err != nil || !info.IsDir() {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Directory not found.")
				continue
			}
			sess.CWD = target
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory changed.")

		case tcpctrl.CmdTYPE:
			if len(args) > 0 && (args[0] == 'A' || args[0] == 'a') {
				sess.TransferType = 'A'
			} else if len(args) > 0 && (args[0] == 'I' || args[0] == 'i') {
				sess.TransferType = 'I'
			} else {
				tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "TYPE must be A or I.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "Type set.")

		case tcpctrl.CmdPASV:
			udpAddr, err := net.ResolveUDPAddr("udp", ":0")
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "Cannot bind UDP.")
				continue
			}
			udpConn, err := net.ListenUDP("udp", udpAddr)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "Cannot bind UDP.")
				continue
			}
			if sess.DataConn != nil {
				sess.DataConn.Close()
			}
			sess.DataConn = udpConn

			port := udpConn.LocalAddr().(*net.UDPAddr).Port
			host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
			ip := net.ParseIP(host)
			if ip == nil {
				ip = net.ParseIP("127.0.0.1")
			}
			ip4 := ip.To4()
			if ip4 == nil {
				ip4 = net.ParseIP("127.0.0.1").To4()
			}
			p1 := port / 256
			p2 := port % 256
			tcpctrl.WriteReply(conn, tcpctrl.ReplyPASV,
				fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d).",
					ip4[0], ip4[1], ip4[2], ip4[3], p1, p2))

		case tcpctrl.CmdLIST:
			target := fs.ResolvePath(sess, args)
			absRoot, _ := filepath.Abs(root)
			fullPath := filepath.Join(absRoot, target)
			infos, err := fs.ListDir(fullPath)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot list directory.")
				continue
			}
			listing := fs.FormatList(infos)

			if sess.DataConn == nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV first.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK, "Opening data connection for directory listing.")

			serverSender := rdt.NewSender(sess.DataConn, sess.ID)
			if err := serverSender.Accept(); err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "RDT handshake failed.")
				continue
			}
			serverSender.Send([]byte(listing))
			serverSender.Close()

			tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Directory listing complete.")

		case tcpctrl.CmdRETR:
			target := fs.ResolvePath(sess, args)
			absRoot, _ := filepath.Abs(root)
			fullPath := filepath.Join(absRoot, target)
			file, info, err := fs.OpenForRead(fullPath)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, fmt.Sprintf("Cannot open %s.", args))
				continue
			}
			defer file.Close()

			if sess.DataConn == nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV first.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK,
				fmt.Sprintf("Opening data connection for %s (%d bytes).", args, info.Size()))

			serverSender := rdt.NewSender(sess.DataConn, sess.ID)
			if err := serverSender.Accept(); err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "RDT handshake failed.")
				continue
			}

			buf := make([]byte, 1400)
			for {
				n, err := file.Read(buf)
				if n > 0 {
					if sendErr := serverSender.Send(buf[:n]); sendErr != nil {
						log.Printf("[CTRL] send error: %v", sendErr)
						break
					}
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					log.Printf("[CTRL] read error: %v", err)
					serverSender.Close()
					tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
					return
				}
			}
			serverSender.Close()
			tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")

		case tcpctrl.CmdSTOR:
			target := fs.ResolvePath(sess, args)
			absRoot, _ := filepath.Abs(root)
			fullPath := filepath.Join(absRoot, target)
			file, err := fs.OpenForWrite(fullPath)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, fmt.Sprintf("Cannot create %s.", args))
				continue
			}
			defer file.Close()

			if sess.DataConn == nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV first.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK,
				fmt.Sprintf("Opening data connection for %s.", args))

			serverReceiver := rdt.NewReceiver(sess.DataConn, sess.ID)
			if err := serverReceiver.Accept(); err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "RDT handshake failed.")
				continue
			}

			for chunk := range serverReceiver.Receive() {
				if _, err := file.Write(chunk); err != nil {
					log.Printf("[CTRL] write error: %v", err)
					tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
					return
				}
			}

			tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")

		default:
			tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "Unknown command.")
		}
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "[server] add TCP control plane and RDT data dispatch"
```

---

## Task 14: Client CLI

**Files:**
- Create: `cmd/client/main.go`

**Interfaces:**
- Consumes: `tcpctrl`, `rdt` packages
- Produces: standalone client binary

- [ ] **Step 1: Write client main.go**

Create `cmd/client/main.go`:

```go
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/tcpctrl"
)

var (
	serverHost = flag.String("host", "127.0.0.1", "Server host")
	ctrlPort   = flag.String("ctrl", "2121", "Control port")
)

func main() {
	flag.Parse()

	addr := net.JoinHostPort(*serverHost, *ctrlPort)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("cannot connect to %s: %v", addr, err)
	}
	defer conn.Close()

	greeting, err := tcpctrl.ReadLine(conn)
	if err != nil {
		log.Fatalf("read greeting: %v", err)
	}
	fmt.Println(greeting)

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("ftp> ")
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			fmt.Print("ftp> ")
			continue
		}

		upper := strings.ToUpper(line)

		// local commands
		if strings.HasPrefix(upper, "LCD ") {
			path := strings.TrimSpace(line[4:])
			if err := os.Chdir(path); err != nil {
				fmt.Printf("lcd: %v\n", err)
			}
			fmt.Print("ftp> ")
			continue
		}
		if upper == "LLS" || strings.HasPrefix(upper, "LLS ") {
			path := "."
			if len(line) > 4 {
				path = strings.TrimSpace(line[4:])
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				fmt.Printf("lls: %v\n", err)
			} else {
				for _, e := range entries {
					info, _ := e.Info()
					if e.IsDir() {
						fmt.Printf("%s/\n", e.Name())
					} else {
						fmt.Printf("%-20s %8d\n", e.Name(), info.Size())
					}
				}
			}
			fmt.Print("ftp> ")
			continue
		}
		if upper == "LPWD" {
			wd, _ := os.Getwd()
			fmt.Println(wd)
			fmt.Print("ftp> ")
			continue
		}

		// send to server
		if _, err := io.WriteString(conn, line+"\r\n"); err != nil {
			log.Fatalf("write error: %v", err)
		}

		cmd, _ := tcpctrl.ParseCommand(line)

		switch cmd {
		case tcpctrl.CmdPASV:
			reply, _ := tcpctrl.ReadLine(conn)
			fmt.Println(reply)
			if strings.HasPrefix(reply, "227") {
				ip, port, parseErr := parsePASV(reply)
				if parseErr != nil {
					fmt.Printf("PASV parse error: %v\n", parseErr)
				} else {
					setupPASVConnection(ip, port)
				}
			}

		case tcpctrl.CmdLIST:
			handleList(conn)

		case tcpctrl.CmdRETR:
			handleRETR(conn, strings.TrimSpace(line[5:]))

		case tcpctrl.CmdSTOR:
			handleSTOR(conn, strings.TrimSpace(line[5:]))

		case tcpctrl.CmdQUIT:
			reply, _ := tcpctrl.ReadLine(conn)
			fmt.Println(reply)
			return

		default:
			reply, err := tcpctrl.ReadLine(conn)
			if err != nil {
				fmt.Printf("read reply error: %v\n", err)
				return
			}
			fmt.Println(reply)
		}

		fmt.Print("ftp> ")
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scanner error: %v", err)
	}
}

var (
	currentPASVHost string
	currentPASVPort int
)

func parsePASV(reply string) (string, int, error) {
	start := strings.Index(reply, "(")
	end := strings.LastIndex(reply, ")")
	if start == -1 || end == -1 || start >= end {
		return "", 0, fmt.Errorf("invalid PASV reply: %s", reply)
	}
	parts := strings.Split(reply[start+1:end], ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("invalid PASV address: %s", reply)
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return "", 0, fmt.Errorf("invalid PASV number %s: %v", p, err)
		}
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]
	return host, port, nil
}

func setupPASVConnection(host string, port int) {
	currentPASVHost = host
	currentPASVPort = port
}

func handleList(conn net.Conn) {
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	sessionID := uint32(1)
	r := rdt.NewReceiver(udpConn, sessionID)
	if err := r.Connect(udpAddr); err != nil {
		fmt.Printf("RDT connect error: %v\n", err)
		return
	}

	var listing []byte
	for chunk := range r.Receive() {
		listing = append(listing, chunk...)
	}
	fmt.Print(string(listing))

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleRETR(conn net.Conn, filename string) {
	if filename == "" {
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	sessionID := uint32(1)
	receiver := rdt.NewReceiver(udpConn, sessionID)
	if err := receiver.Connect(udpAddr); err != nil {
		fmt.Printf("RDT connect error: %v\n", err)
		return
	}

	localFile, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Cannot create local file: %v\n", err)
		receiver.Close()
		return
	}
	defer localFile.Close()

	total := 0
	for chunk := range receiver.Receive() {
		n, _ := localFile.Write(chunk)
		total += n
	}

	fmt.Printf("Downloaded %d bytes to %s\n", total, filename)

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleSTOR(conn net.Conn, filename string) {
	if filename == "" {
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	localFile, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Cannot open local file: %v\n", err)
		udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		tmpConn, _ := net.DialUDP("udp", nil, udpAddr)
		tmpConn.Close()
		tcpctrl.ReadLine(conn) // consume 226
		return
	}
	defer localFile.Close()

	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	sessionID := uint32(1)
	sender := rdt.NewSender(udpConn, sessionID)
	if err := sender.Connect(udpAddr); err != nil {
		fmt.Printf("RDT connect error: %v\n", err)
		return
	}

	buf := make([]byte, 1400)
	total := 0
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if sendErr := sender.Send(buf[:n]); sendErr != nil {
				fmt.Printf("Send error: %v\n", sendErr)
				break
			}
			total += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	sender.Close()

	fmt.Printf("Uploaded %d bytes from %s\n", total, filename)

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}
```

Note: Add `"time"` to imports since `time.Sleep` is used in handleSTOR.

- [ ] **Step 2: Verify compilation**

Run: `go build ./cmd/client/...`
Expected: compilation errors — `imported and not used: "net"` etc. Fix imports:

Update imports section in `cmd/client/main.go`:

```go
import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/tcpctrl"
)
```

- [ ] **Step 3: Verify compilation again**

Run: `go build ./cmd/client/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add cmd/client/main.go
git commit -m "[client] add CLI with RDT RETR/STOR integration"
```

---

## Task 15: Integration Test and Fixtures

**Files:**
- Create: `test/fixtures/sample.txt`
- Create: `test/integration/e2e_test.go`

**Interfaces:**
- Consumes: `cmd/server`, `cmd/client` binaries (built with `go build`)
- Produces: end-to-end integration test

- [ ] **Step 1: Create test fixture**

Create `test/fixtures/sample.txt`:

```
Hello, Hybrid FTP!
This is a sample text file for testing upload and download.
It has multiple lines to verify ASCII text handling.

Line 3: The quick brown fox jumps over the lazy dog.
Line 4: 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ
Line 5: Special chars: !@#$%^&*()_+-=[]{}|;':",./<>?
Line 6: This is the end of the sample file.
```

- [ ] **Step 2: Write integration test**

Create `test/integration/e2e_test.go`:

```go
package integration

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hybrid-ftp/internal/tcpctrl"
)

var (
	serverBin string
	clientBin string
	rootDir   string
	usersFile string
	ctrlPort  string
)

func TestMain(m *testing.M) {
	serverBin = filepath.Join(os.TempDir(), "hybrid-ftp-server-test")
	clientBin = filepath.Join(os.TempDir(), "hybrid-ftp-client-test")

	if err := exec.Command("go", "build", "-o", serverBin, "../../cmd/server").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build server: %v\n", err)
		os.Exit(1)
	}
	if err := exec.Command("go", "build", "-o", clientBin, "../../cmd/client").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build client: %v\n", err)
		os.Exit(1)
	}

	rootDir = filepath.Join(os.TempDir(), "hybrid-ftp-test-root")
	os.MkdirAll(rootDir, 0755)

	usersFile = filepath.Join(os.TempDir(), "hybrid-ftp-test-users.conf")
	os.WriteFile(usersFile, []byte(`admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
`), 0644)

	ctrlPort = "22121"
	code := m.Run()

	os.Remove(serverBin)
	os.Remove(clientBin)
	os.RemoveAll(rootDir)
	os.Remove(usersFile)

	os.Exit(code)
}

func startServer(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	cmd := exec.Command(serverBin,
		"-ctrl", ":"+ctrlPort,
		"-root", rootDir,
		"-users", usersFile,
	)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	return cmd, func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
	}
}

func connectAndSend(t *testing.T, conn net.Conn, line string) string {
	t.Helper()
	_, err := io.WriteString(conn, line+"\r\n")
	if err != nil {
		t.Fatalf("send %s: %v", line, err)
	}
	reply, err := tcpctrl.ReadLine(conn)
	if err != nil {
		t.Fatalf("read reply for %s: %v", line, err)
	}
	return reply
}

func TestE2E_LoginAndPWD(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", ":"+ctrlPort)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greeting := connectAndSend(t, conn, "")
	if !strings.HasPrefix(greeting, "220") {
		t.Errorf("expected 220 greeting, got %s", greeting)
	}

	reply := connectAndSend(t, conn, "USER admin")
	if !strings.HasPrefix(reply, "331") {
		t.Errorf("expected 331, got %s", reply)
	}

	reply = connectAndSend(t, conn, "PASS admin")
	if !strings.HasPrefix(reply, "230") {
		t.Errorf("expected 230, got %s", reply)
	}

	reply = connectAndSend(t, conn, "PWD")
	if !strings.HasPrefix(reply, "257") {
		t.Errorf("expected 257, got %s", reply)
	}

	reply = connectAndSend(t, conn, "QUIT")
	if !strings.HasPrefix(reply, "221") {
		t.Errorf("expected 221, got %s", reply)
	}
}

func TestE2E_AuthFail(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()

	connectAndSend(t, conn, "")
	connectAndSend(t, conn, "USER admin")
	reply := connectAndSend(t, conn, "PASS wrongpass")
	if !strings.HasPrefix(reply, "530") {
		t.Errorf("expected 530, got %s", reply)
	}
}

func TestE2E_UploadDownload(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()

	connectAndSend(t, conn, "")
	connectAndSend(t, conn, "USER admin")
	connectAndSend(t, conn, "PASS admin")

	reply := connectAndSend(t, conn, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("expected 227, got %s", reply)
	}
	pasvHost, pasvPort, err := parsePASVReply(reply)
	if err != nil {
		t.Fatalf("parse PASV: %v", err)
	}

	sampleFile := filepath.Join(rootDir, "uploaded.txt")
	sampleContent := []byte("Hello from integration test!\nThis is a test file.\n")

	if err := os.WriteFile(sampleFile, sampleContent, 0644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	reply = connectAndSend(t, conn, "RETR uploaded.txt")
	if !strings.HasPrefix(reply, "150") {
		t.Fatalf("expected 150, got %s", reply)
	}

	downloaded, err := doRDTDownload(t, pasvHost, pasvPort)
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	if !bytes.Equal(downloaded, sampleContent) {
		t.Errorf("downloaded content mismatch: got %d bytes, want %d bytes", len(downloaded), len(sampleContent))
		t.Logf("got: %q", string(downloaded))
		t.Logf("want: %q", string(sampleContent))
	}

	reply = connectAndSend(t, conn, "")
	if !strings.HasPrefix(reply, "226") {
		t.Errorf("expected 226, got %s", reply)
	}

	connectAndSend(t, conn, "QUIT")
}

func parsePASVReply(reply string) (string, int, error) {
	start := strings.Index(reply, "(")
	end := strings.LastIndex(reply, ")")
	if start == -1 || end == -1 {
		return "", 0, fmt.Errorf("no parentheses in %s", reply)
	}
	parts := strings.Split(reply[start+1:end], ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("expected 6 components, got %d", len(parts))
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n := 0
		fmt.Sscanf(p, "%d", &n)
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]
	return host, port, nil
}

func doRDTDownload(t *testing.T, host string, port int) ([]byte, error) {
	t.Helper()
	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("dial UDP: %w", err)
	}
	defer udpConn.Close()

	sessionID := uint32(1)
	r := rdt.NewReceiver(udpConn, sessionID)
	if err := r.Connect(udpAddr); err != nil {
		return nil, fmt.Errorf("rdt connect: %w", err)
	}

	var data []byte
	for chunk := range r.Receive() {
		data = append(data, chunk...)
	}

	return data, nil
}
```

The test imports `internal/rdt` and `internal/tcpctrl`:

```go
import (
	...
	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/tcpctrl"
)
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./test/integration/...`
Expected: no errors

- [ ] **Step 4: Run the integration test**

Run: `go test ./test/integration/ -v -timeout 60s`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add test/fixtures/sample.txt test/integration/e2e_test.go
git commit -m "[test] add integration test with fixture"
```
