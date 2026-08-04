# Fix B1 and B2 — Design Spec

**Date**: 2026-07-25
**Scope**: Fix review issues B1 (users.conf hash typo) and B2 (PASV single-use)
**Status**: Approved

---

## B1: users.conf Hash Typo

### Problem

`users.conf:6` has `8fb` as the last 3 hex chars of user1's hash instead of `8ee`. The same typo exists in `internal/auth/auth_test.go:10`. This causes `user1:user` login to fail with `530 Login failed.` against the shipped file.

### Fix

| File | Change |
|------|--------|
| `users.conf:6` | `8fb` → `8ee` |
| `internal/auth/auth_test.go:10` | `8fb` → `8ee` |

No additional test needed — the existing `TestCheckPassword_ValidLogin` already covers this path once the fixture hash is corrected.

### Verification

- `go test ./internal/auth/ -v` passes
- Integration test `TestE2E_AuthFail` uses its own test fixture with the correct hash — no change needed

---

## B2: PASV Single-Use → Session-Persistent

### Problem

PASV mode requires re-issuing `PASV` before every data command because the server calls `cleanupDataConn()` after each transfer, which closes the UDP socket and clears `sess.DataMode`. The client's `ensurePASV()` has fragile fallthrough logic that tries to auto-reissue but is coupled to cleanup state.

### Fix: Session-Persistent PASV Socket (Option B from README)

The server keeps the PASV UDP socket open for the session lifetime. Each transfer creates a new RDT sender/receiver on the same `sess.DataConn`. The client retains the PASV address across transfers.

### Server-side Changes (`cmd/server/handlers.go`)

**1. Modify `cleanupDataConn` (lines 83-89)**

Only close the socket for PORT mode. PASV socket persists for session lifetime.

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

**2. Remove `cleanupDataConn` from normal completion paths**

These handlers call it after successful transfer — remove those calls:

| Handler | Line to remove `cleanupDataConn(sess)` |
|---------|---------------------------------------|
| `handleLIST` | 251 |
| `handleRETR` | 323 |
| `handleSTOR` | 383 |
| `handleNLST` | 578 |
| `handleAPPE` | 637 |
| `handleSTOU` | 697 |

**3. Keep `cleanupDataConn` in error/abort paths**

These remain to clean up PORT connections on abort/error:

| Handler | Lines (keep) |
|---------|-------------|
| `handleRETR` | 300, 317 (ABOR, read error) |
| `handleSTOR` | 370, 377 |
| `handleAPPE` | 624, 632 |
| `handleSTOU` | 684, 692 |

**4. No changes needed**

- `handlePASV` (line 165-167): already closes old DataConn before creating new one
- `handlePORT` (line 208-211): already closes PASV DataConn on PORT
- `session.Manager.Unregister` (line 68-69): closes DataConn on session teardown

### Client-side Changes (`cmd/client/main.go`)

**1. Modify `prepareDataTransfer` PASV cleanup (lines 243-247)**

Don't clear PASV address — it persists for session:

```go
case modePASV:
    ...
    cleanup := func() {
        udpConn.Close()
        // PASV address persists for session lifetime
    }
```

**2. Modify `ensurePASV` (lines 212-230)**

Simplify — skip re-issue for established PASV:

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

The PASV-reissuing fallthrough code (lines 219-229) becomes dead code and is removed.

### Data Flow After Fix

```
USER/PASS → PASV (server opens UDP :X, client caches X)
  modePASV set

RETR hello.txt:
  ensurePASV() → returns immediately (modePASV)
  prepareDataTransfer() → fresh local socket → connects to server:X
  RDT handshake → transfer → local socket closed
  server:X stays open

RETR data.bin:
  ensurePASV() → returns immediately (modePASV)
  prepareDataTransfer() → fresh local socket → connects to server:X (same port)
  RDT handshake → transfer → success
```

Why this works: `rdt.NewSender(sess.DataConn, 1)` creates a new sender on the same `*net.UDPConn`. `sender.Accept()` listens for a new SYN, ignoring stale packets from the previous transfer (it filters by `FlagSYN`). The previous sender/receiver goroutines have exited via `Close()`.

### Tests

| Test | Location | What it verifies |
|------|----------|-----------------|
| `TestE2E_PASV_MultiTransfer` | `test/integration/e2e_test.go` | Two consecutive PASV-mode transfers without re-issuing PASV |
| `TestCheckPassword_ValidLogin` (existing) | `internal/auth/auth_test.go` | user1:user authenticates after hash fix |
| `TestE2E_UploadDownload` (existing) | `test/integration/e2e_test.go` | Regressions — one PASV transfer still works |

### Impact Analysis

| Component | Changed? | Risk |
|-----------|----------|------|
| RDT sender/receiver | No | None |
| BBR | No | None |
| TCP control protocol | No | None |
| File system ops | No | None |
| Session manager | No | None |
| Auth | **Yes** (fixture only) | Minimal |
| Server handlers | **Yes** (cleanup patterns) | Medium — verify all data ops still work |
| Client CLI | **Yes** (cleanup + ensurePASV) | Medium — verify both modes |
| PORT mode | No changes to behavior | None |
| ABOR | Kept cleanup in abort paths | Low |
