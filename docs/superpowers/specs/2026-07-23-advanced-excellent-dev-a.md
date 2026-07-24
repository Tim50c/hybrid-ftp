# Advanced + Excellent Level Design — Developer A Features

Date: 2026-07-23
Status: Approved

## 1. Scope

This spec covers all remaining Developer A features to achieve the **Advanced + Excellent** evaluation levels:

| Category | Features |
|----------|----------|
| Missing commands | NOOP, CDUP, MKD, RMD, NLST, STAT, SIZE, MDTM, MODE, PORT, STOU, APPE, DELE, RNFR, RNTO, HASH, ABOR, HELP |
| File system ops | Directory CRUD, file metadata, rename, append, unique store, SHA-256 hash |
| Data channel | PORT (active mode) — server initiates UDP to client |
| Server robustness | Graceful shutdown with 421 broadcast, per-transfer ABOR |
| Code organization | Extract handler functions into `cmd/server/handlers.go` |
| Testing | 14+ new integration tests covering all new features |

## 2. Session Struct Changes

**File**: `internal/session/manager.go`

Add fields to support PORT mode, RNFR/RNTO, and ABOR:

```go
type Session struct {
    ID             uint32
    Conn           net.Conn
    Username       string
    Authenticated  bool
    CWD            string
    DataConn       *net.UDPConn
    DataMode       string           // "PORT" or "PASV"
    DataAddr       *net.UDPAddr     // client's UDP address (PORT mode)
    TransferType   byte
    RnfrPath       string           // stored path from RNFR
    TransferCtx    context.Context  // per-transfer cancel context
    TransferCancel context.CancelFunc
    Ctx            context.Context
    Cancel         context.CancelFunc
}
```

### New Manager Method

```go
func (m *Manager) List() []*Session
```

Returns a snapshot of all active sessions under RLock.

## 3. Command Parser Additions

**File**: `internal/tcpctrl/commands.go`

Add 18 new command constants to the enum and `commandNames` map:

```
CmdNOOP, CmdCDUP, CmdMKD, CmdRMD, CmdNLST, CmdSTAT, CmdSIZE, CmdMDTM,
CmdMODE, CmdPORT, CmdSTOU, CmdAPPE, CmdDELE, CmdRNFR, CmdRNTO, CmdHASH,
CmdABOR, CmdHELP
```

## 4. Reply Code Additions

**File**: `internal/tcpctrl/replies.go`

| Code | Constant | Usage |
|------|----------|-------|
| 211 | ReplySystemStatus | STAT (server status) |
| 212 | ReplyDirStatus | STAT on directory |
| 213 | ReplyFileStatus | SIZE, MDTM, HASH response |
| 214 | ReplyHelp | HELP response |
| 350 | ReplyPendingRNTO | After RNFR |
| 501 | ReplyParamError | Bad arguments |
| 502 | ReplyNotImplCmd | Unimplemented command |
| 553 | ReplyFilenameNotAllowed | Invalid filename |

## 5. File System Operations

**File**: `internal/fs/ops.go`

### New Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `ChangeDir` | `(root string, sess *Session, path string) error` | CWD + CDUP: resolve via ResolveSafe, validate exists & is dir, update sess.CWD |
| `MakeDir` | `(root string, sess *Session, dirname string) error` | MKD: resolve, `os.Mkdir` |
| `RemoveDir` | `(root string, sess *Session, dirname string) error` | RMD: resolve, `os.Remove` (fails if not empty) |
| `DeleteFile` | `(root string, sess *Session, filename string) error` | DELE: resolve, `os.Remove` |
| `NameList` | `(dirPath string) ([]string, error)` | NLST: `os.ReadDir` → names only |
| `FileSize` | `(filePath string) (int64, error)` | SIZE: `os.Stat` → `.Size()` |
| `ModTime` | `(filePath string) (time.Time, error)` | MDTM: `os.Stat` → `.ModTime()` |
| `StatPath` | `(root string, sess *Session, path string) (string, error)` | STAT: returns formatted metadata (name, size, type, modtime, permissions) |
| `OpenForAppend` | `(filePath string) (*os.File, error)` | APPE: `os.OpenFile` with `O_APPEND\|O_CREATE\|O_WRONLY` |
| `RenameFrom` | `(root string, sess *Session, oldname string) error` | RNFR: resolve, verify exists, store in `sess.RnfrPath` |
| `RenameTo` | `(root string, sess *Session, newname string) error` | RNTO: resolve, `os.Rename(sess.RnfrPath, newPath)`, clear RnfrPath |
| `GenerateUniqueName` | `(originalName string) string` | STOU: `fmt.Sprintf("stou_%d_%08x", time.Now().Unix(), rand.Uint32())` + original extension |
| `ComputeHash` | `(filePath string) (string, error)` | HASH: read in 64KB chunks → `sha256.New()` → hex digest |

### Path Safety

All path-taking functions go through `ResolveSafe` to jail within root directory.

### Unit Tests

**File**: `internal/fs/ops_test.go`

Test all new functions within a temp directory root:
- ChangeDir: valid, nonexistent, path traversal
- MakeDir/RemoveDir: create, remove, remove non-empty
- DeleteFile: delete, delete nonexistent
- NameList: directory with files and subdirs
- FileSize/ModTime: known file
- RenameFrom/RenameTo: valid rename, missing source, RNTO without RNFR
- GenerateUniqueName: uniqueness across calls
- ComputeHash: known hash for known content
- OpenForAppend: append to existing, create new

## 6. Handler Functions

**File**: `cmd/server/handlers.go`

Extract all command handlers into standalone functions. Signature pattern:

```go
func handleXXX(conn net.Conn, sess *session.Session, root string, args string)
```

### Handler Table

| Handler | Reply Flow |
|---------|------------|
| `handleNOOP` | `200 NOOP ok.` |
| `handleCDUP` | `fs.ChangeDir(root, sess, "..")` → `250` or `550` |
| `handleMKD` | `fs.MakeDir` → `257 "path" created.` or `550` |
| `handleRMD` | `fs.RemoveDir` → `250` or `550` |
| `handleNLST` | Check data mode → `150` → RDT send names → `226` |
| `handleSTAT` | No args: `211` server status. With path: `212`/`213` metadata |
| `handleSIZE` | `fs.FileSize` → `213 <size>` or `550` |
| `handleMDTM` | `fs.ModTime` → `213 YYYYMMDDhhmmss` or `550` |
| `handleMODE` | S→`200 Mode set.`, B/C→`504 Not implemented.` |
| `handlePORT` | Parse `h1,h2,h3,h4,p1,p2` → store `DataAddr`, `DataMode="PORT"` → `200` |
| `handleDELE` | `fs.DeleteFile` → `250` or `550` |
| `handleRNFR` | `fs.RenameFrom` → `350` or `550` |
| `handleRNTO` | `fs.RenameTo` → `250` or `503` (no RNFR) / `550` |
| `handleSTOU` | Generate unique name → STOR flow with generated name → `257` with filename |
| `handleAPPE` | `fs.OpenForAppend` → `150` → RDT receive → write → `226` |
| `handleHASH` | `fs.ComputeHash` → `213 <hex>` or `550` |
| `handleABOR` | `sess.TransferCancel()` → `226 ABOR successful.` |
| `handleHELP` | No args: `214` with all commands. With arg: `214` specific help text |

### Existing Handlers (also extracted)

`handleUSER`, `handlePASS`, `handleQUIT`, `handlePWD`, `handleCWD`, `handleTYPE`, `handlePASV`, `handleLIST`, `handleRETR`, `handleSTOR`

### Updated `main.go` Dispatch

The switch in `handleSession` becomes one-liners:

```go
switch cmd {
case tcpctrl.CmdUSER:
    handleUSER(conn, sess, usersPath, args)
case tcpctrl.CmdMKD:
    handleMKD(conn, sess, root, args)
// ... etc
default:
    tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "Unknown command.")
}
```

## 7. PORT (Active Mode) Data Connection

### PORT Command Handler

Parse `h1,h2,h3,h4,p1,p2`:
```go
parts := strings.Split(args, ",")
ip := fmt.Sprintf("%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3])
port := p1*256 + p2
sess.DataAddr = &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
sess.DataMode = "PORT"
```

### Data Connection Setup (PORT vs PASV)

Helper function `setupDataSender(sess *Session)` and `setupDataReceiver(sess *Session)`:

**PASV mode** (existing):
- Server already has `sess.DataConn` bound from PASV handler
- If sender: `rdt.NewSender(sess.DataConn, sess.ID)` → `Accept()`
- If receiver: `rdt.NewReceiver(sess.DataConn, sess.ID)` → `Accept()`

**PORT mode** (new):
- Server creates new UDP socket: `net.ListenUDP("udp", &net.UDPAddr{Port: 0})`
- Store in `sess.DataConn`
- If sender: `rdt.NewSender(udpConn, sess.ID)` → `Connect(sess.DataAddr)`
- If receiver: `rdt.NewReceiver(udpConn, sess.ID)` → `Connect(sess.DataAddr)`

### After Transfer

Close `sess.DataConn` and set to nil. Reset `sess.DataMode` to empty.

## 8. ABOR Implementation

### Transfer Context

Before each data transfer (RETR, STOR, LIST, NLST, APPE, STOU):
```go
sess.TransferCtx, sess.TransferCancel = context.WithCancel(sess.Ctx)
defer sess.TransferCancel()
```

### Transfer Loop Integration

File read/write loops check `sess.TransferCtx.Done()`:
```go
select {
case <-sess.TransferCtx.Done():
    // abort transfer
default:
    // continue
}
```

### ABOR Handler

```go
func handleABOR(conn net.Conn, sess *session.Session, root string, args string) {
    if sess.TransferCancel != nil {
        sess.TransferCancel()
    }
    tcpctrl.WriteReply(conn, 226, "ABOR successful.")
}
```

The transfer goroutine detects cancellation, sends RST on UDP, and the main handler sends `426 Transfer aborted` before the `226` from ABOR.

## 9. Graceful Shutdown

### `main()` Changes

```go
var wg sync.WaitGroup

// In accept loop:
wg.Add(1)
go func() {
    defer wg.Done()
    handleSession(conn, mgr, *rootDir, *usersFile)
}()

// Signal handler:
go func() {
    <-sigCh
    log.Printf("[CTRL] shutting down...")
    listener.Close()
    for _, s := range mgr.List() {
        tcpctrl.WriteReply(s.Conn, 421, "Service not available, closing.")
        s.Cancel()
    }
}()

// After accept loop exits:
wg.Wait()
```

## 10. Integration Tests

**File**: `test/integration/e2e_test.go`

| Test | Commands | Validates |
|------|----------|-----------|
| `TestE2E_MKD_RMD` | MKD, RMD, LIST | Create dir, verify via LIST, remove, verify gone |
| `TestE2E_DELE` | DELE, RETR | Delete file, verify 550 on subsequent RETR |
| `TestE2E_CDUP` | CWD, CDUP, PWD | Navigate into subdir, CDUP back, PWD verifies root |
| `TestE2E_NLST` | NLST | Name-only listing contains expected filenames |
| `TestE2E_SIZE_MDTM` | SIZE, MDTM | Correct byte size and YYYYMMDDhhmmss format |
| `TestE2E_RNFR_RNTO` | RNFR, RNTO, RETR | Rename file, old name 550, new name works |
| `TestE2E_STOU` | STOU | Upload, verify unique filename in 257 reply |
| `TestE2E_APPE` | APPE | Append to existing file, verify combined content |
| `TestE2E_HASH` | HASH | SHA-256 matches known value |
| `TestE2E_MODE` | MODE S, MODE B | S→200, B→504 |
| `TestE2E_NOOP_HELP` | NOOP, HELP | 200, 214 |
| `TestE2E_PORT_Transfer` | PORT, RETR | Active mode download via RDT |
| `TestE2E_ConcurrentSessions` | Multiple clients | 2+ simultaneous transfers succeed |
| `TestE2E_ABOR` | ABOR mid-transfer | Transfer aborted, control channel survives |

### Test Fixtures

**Directory**: `test/fixtures/`

- `sample.txt` — small ASCII text file
- `sample.bin` — small binary file (generated in TestMain)
- `subdir/` — nested directory for CWD/LIST tests

## 11. File Summary

| File | Action | Description |
|------|--------|-------------|
| `internal/session/manager.go` | Modify | Add DataMode, DataAddr, RnfrPath, TransferCtx, TransferCancel fields; add List() method |
| `internal/session/manager_test.go` | Modify | Add tests for List() |
| `internal/tcpctrl/commands.go` | Modify | Add 18 new command constants |
| `internal/tcpctrl/commands_test.go` | Modify | Add test cases for new commands |
| `internal/tcpctrl/replies.go` | Modify | Add 8 new reply code constants |
| `internal/tcpctrl/replies_test.go` | Modify | Add new constants to uniqueness test |
| `internal/fs/ops.go` | Modify | Add 13 new functions |
| `internal/fs/ops_test.go` | Create | Unit tests for all FS operations |
| `cmd/server/handlers.go` | Create | All handler functions extracted from main.go |
| `cmd/server/main.go` | Modify | Slim dispatch switch, WaitGroup, graceful shutdown |
| `test/integration/e2e_test.go` | Modify | Add 14 new test functions |
| `test/fixtures/sample.txt` | Create | ASCII test fixture |

## 12. Error Handling Summary

| Condition | Reply |
|-----------|-------|
| MKD/RMD on invalid path | 550 |
| RMD on non-empty directory | 550 |
| DELE on nonexistent file | 550 |
| RNTO without prior RNFR | 503 |
| RNFR on nonexistent file | 550 |
| SIZE/MDTM on directory | 550 |
| SIZE/MDTM on nonexistent | 550 |
| HASH on nonexistent file | 550 |
| PORT with malformed address | 501 |
| MODE B or MODE C | 504 |
| STOU with no data connection | 503 |
| APPE with no data connection | 503 |
| Any auth-gated command before login | 530 |
| ABOR with no active transfer | 226 (no-op) |
| Server shutdown | 421 to all sessions |
