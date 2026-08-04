# Fix B1 and B2 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the two high-severity review issues: B1 (users.conf hash typo preventing user1 login) and B2 (PASV single-use requiring re-issue before every data command).

**Architecture:** B1 is a 2-line fix in `users.conf` and `auth_test.go`. B2 makes the PASV data connection session-persistent: the server keeps the UDP socket open across transfers, and the client retains the PASV address without clearing it. Each transfer creates a new RDT sender/receiver on the same `*net.UDPConn`. PORT mode behavior is unchanged.

**Tech Stack:** Go 1.21+, standard library only (net, crypto, os, etc.)

## Global Constraints

- NO external libraries for networking, FTP, or reliable transport
- Every error must be checked; use `fmt.Errorf("context: %w", err)` for wrapping
- Logging with `log.Printf()` and `[CTRL]`, `[RDT]`, `[FS]`, `[SESS]`, `[AUTH]` prefixes
- All shared maps protected by `sync.RWMutex`
- Commit format: `fix(scope): short description`

---

### Task 1: B1 — Fix users.conf hash typo

**Files:**
- Modify: `users.conf:6`

- [ ] **Step 1: Edit users.conf line 6**

Change the last 3 hex chars from `8fb` to `8ee`:

```
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
```

- [ ] **Step 2: Verify the fix**

```bash
Get-Content users.conf
```

Expected: line 6 ends with `df8ee`, not `df8fb`

---

### Task 2: B1 — Fix auth_test.go hash and run tests

**Files:**
- Modify: `internal/auth/auth_test.go:10`

- [ ] **Step 1: Edit auth_test.go line 10**

Change `8fb` to `8ee` in the test fixture:

```go
const testUsersConf = `# test credentials
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
`
```

- [ ] **Step 2: Run auth unit tests**

```bash
go test ./internal/auth/ -v
```

Expected: all tests PASS, including `TestCheckPassword_ValidLogin` which checks `user1:user`

- [ ] **Step 3: Commit B1 fix**

```bash
git add users.conf internal/auth/auth_test.go
git commit -m "fix(auth): correct user1 hash in users.conf (8fb -> 8ee)"
```

---

### Task 3: B2 Server — Modify cleanupDataConn for PORT-only cleanup

**Files:**
- Modify: `cmd/server/handlers.go:83-89`

**Interfaces:**
- Consumes: `*session.Session` (DataConn, DataMode, DataAddr fields)
- Produces: `func cleanupDataConn(sess *session.Session)` — now closes socket only for PORT mode

- [ ] **Step 1: Replace cleanupDataConn function**

Replace lines 83-89 with:

```go
func cleanupDataConn(sess *session.Session) {
	if sess.DataMode == "PORT" {
		if sess.DataConn != nil {
			sess.DataConn.Close()
			sess.DataConn = nil
		}
		sess.DataMode = ""
		sess.DataAddr = nil
	}
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./cmd/server/...
```

Expected: no errors

---

### Task 4: B2 Server — Remove cleanupDataConn from normal completion paths

**Files:**
- Modify: `cmd/server/handlers.go` (6 lines removed)

**Interfaces:**
- Consumes: `cleanupDataConn(sess)` function from Task 3 (now PORT-only)
- Produces: All 6 data handlers no longer close PASV socket after successful transfer

- [ ] **Step 1: Remove cleanupDataConn from handleLIST normal path (line 251)**

Remove the line `	cleanupDataConn(sess)` at line 251 (between `serverSender.Close()` and `tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, ...)`).

Before:
```go
	serverSender.Send([]byte(listing))
	serverSender.Close()
	cleanupDataConn(sess)

	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Directory listing complete.")
```

After:
```go
	serverSender.Send([]byte(listing))
	serverSender.Close()

	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Directory listing complete.")
```

- [ ] **Step 2: Remove cleanupDataConn from handleRETR normal path (line 323)**

Remove line 323 `	cleanupDataConn(sess)` (lines 322-324 become):

Before:
```go
	serverSender.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
```

After:
```go
	serverSender.Close()
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
```

- [ ] **Step 3: Remove cleanupDataConn from handleSTOR normal path (line 383)**

Remove line 383 `	cleanupDataConn(sess)` (lines 382-385 become):

Before:
```go
	serverReceiver.Close()
	cleanupDataConn(sess)

	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
```

After:
```go
	serverReceiver.Close()

	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
```

- [ ] **Step 4: Remove cleanupDataConn from handleNLST normal path (line 578)**

Remove line 578 `	cleanupDataConn(sess)` (lines 577-579 become):

Before:
```go
	sender.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Name list complete.")
```

After:
```go
	sender.Close()
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Name list complete.")
```

- [ ] **Step 5: Remove cleanupDataConn from handleAPPE normal path (line 637)**

Remove line 637 `	cleanupDataConn(sess)` (lines 636-638 become):

Before:
```go
	receiver.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
```

After:
```go
	receiver.Close()
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
```

- [ ] **Step 6: Remove cleanupDataConn from handleSTOU normal path (line 697)**

Remove line 697 `	cleanupDataConn(sess)` (lines 696-698 become):

Before:
```go
	receiver.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" stored.", uniqueName))
```

After:
```go
	receiver.Close()
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" stored.", uniqueName))
```

- [ ] **Step 7: Verify that abort/error paths still have cleanupDataConn**

Run `go vet` and verify:

```bash
go vet ./cmd/server/...
go build ./cmd/server/...
```

Expected: no errors. The following lines should still have `cleanupDataConn(sess)`:
- 300 (RETR ABOR path)
- 317 (RETR read error)
- 370 (STOR ABOR path)
- 377 (STOR write error)
- 624 (APPE ABOR path)
- 632 (APPE write error)
- 684 (STOU ABOR path)
- 692 (STOU write error)

---

### Task 5: B2 Client — Don't clear PASV address in cleanup

**Files:**
- Modify: `cmd/client/main.go:243-247`

**Interfaces:**
- Consumes: `currentPASVHost`, `currentPASVPort` (package-level vars)
- Produces: PASV address persists across transfers

- [ ] **Step 1: Remove the two lines that clear currentPASVHost and currentPASVPort**

Change the PASV cleanup closure in `prepareDataTransfer` (lines 243-247):

Before:
```go
		cleanup := func() {
			udpConn.Close()
			currentPASVHost = ""
			currentPASVPort = 0
		}
```

After:
```go
		cleanup := func() {
			udpConn.Close()
		}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./cmd/client/...
```

Expected: no errors

---

### Task 6: B2 Client — Simplify ensurePASV

**Files:**
- Modify: `cmd/client/main.go:212-230`

**Interfaces:**
- Consumes: `connDataMode`, `modePORT`, `modePASV`, `modeNone` (package-level)
- Produces: `func ensurePASV(conn net.Conn)` — returns immediately for established PASV

- [ ] **Step 1: Replace ensurePASV function**

Replace lines 212-230 with:

```go
func ensurePASV(conn net.Conn) {
	if connDataMode == modePORT || connDataMode == modePASV {
		return
	}
	if connDataMode == modeNone {
		return
	}
}
```

Remove the dead code block (`io.WriteString(conn, "PASV\r\n")` through `}`) since ensurePASV no longer re-issues PASV.

- [ ] **Step 2: Verify compilation**

```bash
go build ./cmd/client/...
```

Expected: no errors. Also verify `go vet`:

```bash
go vet ./cmd/client/...
```

Expected: no warnings about unused code.

---

### Task 7: Write integration test — two RETR transfers without re-PASV

**Files:**
- Modify: `test/integration/e2e_test.go` (append new test)
- Create: `test/integration/` (test fixtures created at runtime)

**Interfaces:**
- Consumes: `startServer`, `newFTPConn`, `ftpConn` (sendAndRead, readReply), `parsePASVReply`, `doRDTDownload`, `parsePASVReply` (all from e2e_test.go)
- Produces: `TestE2E_PASV_MultiTransfer` — verifies PASV persists across two transfers

- [ ] **Step 1: Add the test to the end of e2e_test.go**

```go
// TestE2E_PASV_MultiTransfer verifies that the PASV UDP socket persists
// across multiple data commands without re-issuing PASV (fix for B2).
func TestE2E_PASV_MultiTransfer(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "first.txt"), []byte("first file content\n"), 0644)
	os.WriteFile(filepath.Join(rootDir, "second.txt"), []byte("second file content\n"), 0644)

	reply := fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("PASV: expected 227, got %s", reply)
	}
	host, port, err := parsePASVReply(reply)
	if err != nil {
		t.Fatalf("parse PASV: %v", err)
	}

	// First transfer
	r := fc.sendAndRead(t, "RETR first.txt")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("first RETR: expected 150, got %s", r)
	}
	data1, err := doRDTDownload(t, host, port)
	if err != nil {
		t.Fatalf("first download: %v", err)
	}
	if !bytes.Equal(data1, []byte("first file content\n")) {
		t.Errorf("first file mismatch: got %q", string(data1))
	}
	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)

	// Second transfer — no PASV re-issue (B2 fix)
	r = fc.sendAndRead(t, "RETR second.txt")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("second RETR: expected 150, got %s", r)
	}
	data2, err := doRDTDownload(t, host, port)
	if err != nil {
		t.Fatalf("second download: %v", err)
	}
	if !bytes.Equal(data2, []byte("second file content\n")) {
		t.Errorf("second file mismatch: got %q", string(data2))
	}
	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)

	fc.sendAndRead(t, "QUIT")
}
```

- [ ] **Step 2: Run the new integration test**

```bash
go test ./test/integration/ -v -run TestE2E_PASV_MultiTransfer -timeout 60s
```

Expected: PASS

---

### Task 8: Write integration test — PASV STOR then RETR without re-PASV

**Files:**
- Modify: `test/integration/e2e_test.go` (append new test)

- [ ] **Step 1: Add the test**

```go
// TestE2E_PASV_UploadThenDownload verifies PASV persistence across an
// upload followed by a download without re-issuing PASV.
func TestE2E_PASV_UploadThenDownload(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	payload := []byte("upload then download test payload\n")
	os.WriteFile(filepath.Join(rootDir, "ref.txt"), payload, 0644)

	reply := fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("PASV: expected 227, got %s", reply)
	}
	host, port, _ := parsePASVReply(reply)

	// STOR — upload via PASV
	r := fc.sendAndRead(t, "STOR up.bin")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("STOR: expected 150, got %s", r)
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	sender := rdt.NewSender(udpConn, 1)
	sender.Connect(udpAddr)
	sender.Send(payload)
	time.Sleep(200 * time.Millisecond)
	sender.Close()
	udpConn.Close()

	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)

	// RETR the same file — no PASV re-issue (B2 fix)
	r = fc.sendAndRead(t, "RETR up.bin")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("RETR after STOR: expected 150, got %s", r)
	}
	data, err := doRDTDownload(t, host, port)
	if err != nil {
		t.Fatalf("download after upload: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("data mismatch: got %d bytes, want %d bytes", len(data), len(payload))
	}
	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)

	fc.sendAndRead(t, "QUIT")
}
```

- [ ] **Step 2: Run the new integration test**

```bash
go test ./test/integration/ -v -run TestE2E_PASV_UploadThenDownload -timeout 60s
```

Expected: PASS

---

### Task 9: Run full test suite

- [ ] **Step 1: Run all unit tests**

```bash
go test ./internal/... -v
```

Expected: all packages pass, 0 failures

- [ ] **Step 2: Build server and client**

```bash
go build ./cmd/server/...
go build ./cmd/client/...
```

Expected: no errors

- [ ] **Step 3: Run all integration tests**

```bash
go test ./test/integration/... -v -timeout 180s
```

Expected: all tests pass. Verify the multi-transfer tests (Tasks 7-8) pass along with existing tests.

- [ ] **Step 4: Run vet**

```bash
go vet ./...
```

Expected: no findings

---

### Task 10: Commit B2 fix

- [ ] **Step 1: Stage and commit**

```bash
git add cmd/server/handlers.go cmd/client/main.go test/integration/e2e_test.go
git commit -m "fix(ctrl): make PASV data socket session-persistent (B2)"
```

Expected: commit succeeds with all B2 changes in one atomic commit

- [ ] **Step 2: Verify final state**

```bash
git log --oneline -3
```

Expected: two commits visible — one for B1, one for B2
