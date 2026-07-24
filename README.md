# Hybrid FTP

A custom FTP server and client that decouples the control plane (TCP) from the data plane — using a custom Reliable Data Transfer (RDT) protocol over UDP with BBR congestion control.

**Language:** Go 1.21+ / **Dependencies:** Standard library only

---

## Architecture

```
 Client                          Server
 ┌──────────────┐               ┌──────────────┐
 │ TCP Control  │───TCP:2121───│  TCP Control  │  Commands / Replies
 │ CLI Handler  │◄──responses──│    Dispatcher  │
 └──────────────┘               └─────┬─────────┘
 ┌──────────────┐                     │
 │ RDT Data     │───UDP:data───│  RDT Data      │  File Transfers
 │ Sender/Recv  │◄──UDP:data───│  Sender/Recv   │
 └──────────────┘               └────────────────┘
```

| Layer | Protocol | Role |
|-------|----------|------|
| Control Channel | TCP | FTP commands (USER, PASV, RETR, STOR, HASH, ...) and reply codes |
| Data Channel | RDT over UDP | File payload with Selective Repeat ARQ and BBR congestion control |

---

## Build

```bash
git clone https://github.com/Tim50c/hybrid-ftp.git
cd hybrid-ftp

go build ./cmd/server   # → ./server
go build ./cmd/client   # → ./client
```

---

## Server

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-ctrl` | `:2121` | TCP control port |
| `-root` | `/tmp/hybrid-ftp` | Virtual filesystem root |
| `-users` | `users.conf` | Path to credentials file |

### Running

```bash
./server -ctrl :2121 -root ./data -users users.conf
```

### Authentication

Credentials stored in `users.conf` with SHA-256 hashed passwords:

```
# Format: username:SHA-256-hex-hash
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
```

Default users: `admin:admin`, `user1:user`

> **⚠️ Known issue (B1)**: As shipped, `users.conf` line 6 has a typo in the hash for `user1` (ends in `8fb` instead of `8ee`), so `user1:user` returns `530 Login failed.` against the shipped file. All other artifacts (README, docs, tests) use the correct hash. See [Known Issues](#d-known-issues-found-during-review) in the Manual Test Plan for the one-line fix.

---

## Client

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-host` | `127.0.0.1` | Server address |
| `-ctrl` | `2121` | Control port |

### Running

```bash
./client -host 127.0.0.1 -ctrl 2121
```

### Local Commands

| Command | Description |
|---------|-------------|
| `lcd <path>` | Change local working directory |
| `lls [path]` | List local directory contents |
| `lpwd` | Print local working directory |

### FTP Commands

| Command | Description |
|---------|-------------|
| `USER <name>` / `PASS <pw>` | Login (SHA-256 authentication) |
| `PWD` | Print server working directory |
| `CWD <path>` | Change server working directory |
| `CDUP` | Change to parent directory |
| `MKD <name>` / `RMD <name>` | Create / Remove directory |
| `DELE <name>` | Delete file |
| `LIST [path]` | Detailed directory listing |
| `NLST [path]` | Name-only directory listing |
| `STAT [path]` | File/directory status |
| `SIZE <name>` / `MDTM <name>` | File size / modification time |
| `PASV` | Enter passive mode (server opens data port) |
| `PORT h1,h2,h3,h4,p1,p2` | Enter active mode (client opens data port) |
| `RETR <file>` | Download file |
| `STOR <file>` | Upload file |
| `STOU` | Upload file with unique name |
| `APPE <file>` | Append to file |
| `RNFR <old>` / `RNTO <new>` | Rename file |
| `HASH <file>` | SHA-256 hash comparison (local vs remote) |
| `TYPE A` / `TYPE I` | Set transfer mode (ASCII / Binary) |
| `MODE S` / `MODE B` / `MODE C` | Set transfer mode (Stream / Block / Compressed) |
| `NOOP` | No operation (keep-alive) |
| `HELP` | List available commands |
| `ABOR` | Abort active transfer |
| `QUIT` | Disconnect |

---

## RDT Protocol

### Header (21 bytes)

| Offset | Size | Field | Type |
|--------|------|-------|------|
| 0 | 4 | Session ID | uint32 |
| 4 | 4 | Sequence Number | uint32 |
| 8 | 4 | ACK Number | uint32 |
| 12 | 1 | Flags | uint8 |
| 13 | 2 | Payload Length | uint16 |
| 15 | 2 | Window Size | uint16 |
| 17 | 4 | Checksum | uint32 |

### Flags

| Bit | Mask | Name | Meaning |
|-----|------|------|---------|
| 0 | 0x01 | SYN | Connection initiation |
| 1 | 0x02 | FIN | Graceful close |
| 2 | 0x04 | ACK | Acknowledgment |
| 3 | 0x08 | RST | Abrupt reset |
| 4 | 0x10 | DATA | Payload follows |

### Features

- **Selective Repeat ARQ** — per-packet timers with exponential backoff (500ms → 60s cap, max 3 retransmits)
- **CRC32-IEEE checksum** — computed over header + payload; corrupted packets silently discarded
- **3-way handshake** — SYN → SYN+ACK → ACK for connection establishment
- **Sliding window** — in-order delivery to application; out-of-order packets buffered

### BBR Congestion Control

Model-based controller with 4 states:

| State | Pacing Gain | Behavior |
|-------|-------------|----------|
| STARTUP | 2.89 | Exponential bandwidth probing; exits after 3 RTTs of plateauing throughput |
| DRAIN | 0.75 | Drains queue built during startup; exits when inflight ≤ BDP |
| PROBE_BW | cyclic [1.25, 0.75, 1.0] | 8-phase steady state probing to detect bandwidth increases |
| PROBE_RTT | 4×MSS for 200ms | Every 10s, probes for new minimum RTT (route change detection) |

Key variables: **Btlbw** (bottleneck bandwidth estimate, max over 10 RTT rounds), **RTprop** (minimum RTT over 10-second window), **cwnd** = pacing_gain × Btlbw × RTprop (floor: 4×MSS).

---

## Testing

```bash
# Unit tests
go test ./internal/... -v

# Integration tests (requires build of server + client)
go test ./test/integration/... -v -timeout 180s

# Static analysis
go vet ./...
```

### Appendix A — Quick Reference Examples

The narrative walkthrough below is kept as a quick-start reference. For a structured, exhaustive test plan with pass/fail criteria see [Manual Test Plan](#manual-test-plan).

#### 1. Start the Server

```bash
# Terminal 1
mkdir -p /tmp/hybrid-ftp
echo "hello from hybrid ftp" > /tmp/hybrid-ftp/hello.txt
echo "binary content here" > /tmp/hybrid-ftp/data.bin
mkdir /tmp/hybrid-ftp/subdir
echo "nested file" > /tmp/hybrid-ftp/subdir/nested.txt

go run ./cmd/server -ctrl :2121 -root /tmp/hybrid-ftp -users users.conf
```

#### 2. Start the Client

```bash
# Terminal 2
go run ./cmd/client -host 127.0.0.1 -ctrl 2121
```

#### 3. Data Mode Selection

After login, the client has no mode set. Data commands will prompt you to pick one:

```
ftp> LIST
Use PASV or PORT first.
ftp> PASV
227 Entering Passive Mode (127,0,0,1,xxx,xxx).   # now in PASV mode
ftp> LIST
150 Opening data connection for directory listing. # works
...
```

> **⚠️ Known issue (B2)**: The README originally claimed "subsequent LIST/RETR/STOR auto-send PASV when needed". In the current implementation, PASV is **single-use on both sides**: the server closes its UDP socket after every transfer (`cleanupDataConn`) and the client's `ensurePASV()` only fires when the previous PASV was never parsed. **Re-issue `PASV` before every data command in passive mode.** See [Known Issues](#d-known-issues-found-during-review) for details.

Switch to PORT mode and back:

```
ftp> PORT 127,0,0,1,200,0
200 PORT command successful.                      # now in PORT mode
ftp> RETR hello.txt
150 Opening data connection for hello.txt (21 bytes).
...
ftp> PASV
227 Entering Passive Mode (127,0,0,1,xxx,xxx).   # back to PASV mode
ftp> PASV                                          # re-issue before each data op
ftp> LIST
...
```

PORT sockets are also single-use — re-send `PORT` before each transfer in active mode.

#### 4. Login and Navigation

```
ftp> USER admin
331 Password required.
ftp> PASS admin
230 Login successful.
ftp> PWD
257 "/"
ftp> CWD subdir
250 Directory changed to /subdir.
ftp> PWD
257 "/subdir"
ftp> CDUP
250 Directory changed to parent.
```

#### 5. Directory Operations

```
ftp> PASV
ftp> MKD newdir
257 "/newdir" created.
ftp> LIST
150 Opening data connection for directory listing.
226 Directory listing complete.
ftp> RMD newdir
250 "/newdir" removed.
```

#### 6. Download a File (RETR)

```
ftp> PASV
ftp> RETR hello.txt
150 Opening data connection for hello.txt (21 bytes).
██████████████████████████████ 100% | 2.0 MiB/s | ETA: 00:00
226 Transfer complete.
```

#### 7. Upload a File (STOR)

```bash
echo "uploaded from client" > upload.txt
```

```
ftp> PASV
ftp> STOR upload.txt
150 Opening data connection for upload.txt.
██████████████████████████████ 100% | 1.8 MiB/s | ETA: 00:00
226 Transfer complete.
```

#### 8. Hash Verification

```
ftp> HASH hello.txt
213 SHA-256 0-5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03 hello.txt
Hashes match - file integrity verified.
```

#### 9. PORT Mode (Active)

```
ftp> PORT 127,0,0,1,200,0
200 PORT command successful.
ftp> RETR hello.txt
150 Opening data connection for hello.txt (21 bytes).
██████████████████████████████ 100% | 2.0 MiB/s | ETA: 00:00
226 Transfer complete.

ftp> RETR data.bin                                  # PORT used up — need re-PORT
ftp> PORT 127,0,0,1,200,1
200 PORT command successful.
ftp> RETR data.bin
150 Opening data connection for data.bin.
...
```

#### 10. Large File Transfer (Progress Bar)

```bash
dd if=/dev/urandom of=/tmp/hybrid-ftp/bigfile.bin bs=1M count=50
```

```
ftp> PASV
ftp> RETR bigfile.bin
████████████████████████░░░░░░░░ 65% | 3.5 MiB/s | ETA: 00:05
```

#### 11. Concurrent Sessions

Open two terminals and run the client in each. Each sets PASV (or PORT) independently and transfers proceed in parallel:

```
Terminal 1:                    Terminal 2:
ftp> PASV                      ftp> PASV
ftp> RETR hello.txt            ftp> RETR data.bin
226 Transfer complete.         226 Transfer complete.
```

#### 12. Local Client Commands

```
ftp> lpwd
/home/user
ftp> lls
upload.txt  hello.txt  data.bin
ftp> lcd /tmp
ftp> lpwd
/tmp
ftp> lls
hybrid-ftp  other-files
```

---

## Manual Test Plan

This section is the **exhaustive, structured manual test plan** that fully covers the project's features, requirements (`REQUIREMENTS.md §1.3`, §2.2, §2.3), and the implemented RFC 959 subset. Every test case has a stable ID, pre-requisites, steps, expected result, and pass criterion.

### Conventions

- **TC-XX-NN** = Test Case in section XX, case NN.
- Server log prefixes to grep: `[CTRL]`, `[RDT]`, `[FS]`, `[SESS]`, `[AUTH]`.
- Reply-code references follow `REQUIREMENTS.md §2.3` (1xx preliminary, 2xx completion, 3xx intermediate, 4xx transient, 5xx permanent).
- Prompt shown as `ftp> ` (client) and `> ` (server log).
- For destructive tests (DELE, RMD, RNFR/RNTO), always use throwaway files inside the test root.
- **Operating system**: most tests work on Linux/macOS/Windows. Tests that need `iptables`, symlinks, or `/dev/urandom` are marked.

### TC-01 — Pre-test Setup & Environment

| ID | Objective | Pre-reqs | Steps | Expected | Pass criterion |
|----|-----------|----------|-------|----------|----------------|
| TC-01-01 | Go toolchain present | — | `go version` | `go1.21` or newer | Exit 0, version ≥ 1.21 |
| TC-01-02 | Repository builds clean | TC-01-01 | `go vet ./...` | No output, exit 0 | `go vet` returns 0 |
| TC-01-03 | Build server & client | TC-01-02 | `go build ./cmd/server ./cmd/client` | `server` and `client` binaries (or `.exe` on Windows) | Both exist, runnable |
| TC-01-04 | Create test fixtures | TC-01-03 | See fixture script in Pre-test Setup | 5 files: small ASCII, 50 MB binary, 100 MB binary, deep tree, symlink | All files present with correct sizes |
| TC-01-05 | Confirm users.conf parses | TC-01-03 | `cat users.conf` | Two user entries; lines starting with `#` are comments | File matches format spec |

**Fixture creation script** (cross-platform, run once):

```bash
# Linux / macOS / WSL
ROOT=/tmp/hybrid-ftp
mkdir -p $ROOT/sub1/sub2/sub3
echo "hello from hybrid ftp" > $ROOT/hello.txt
echo "binary content" > $ROOT/data.bin
dd if=/dev/urandom of=$ROOT/big50.bin  bs=1M count=50 status=none
dd if=/dev/urandom of=$ROOT/big100.bin bs=1M count=100 status=none
# symlink to outside the root (Linux/macOS)
ln -sf /etc/passwd $ROOT/escape.lnk
# Windows (PowerShell) equivalent — create symlink requires admin / developer mode
# New-Item -ItemType SymbolicLink -Path "$ROOT\escape.lnk" -Target "C:\Windows\win.ini"
```

### TC-02 — Static / Build / Unit Tests

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-02-01 | Static analysis | `go vet ./...` | No findings | Exit 0 |
| TC-02-02 | Unit tests pass | `go test ./internal/... -v` | All packages pass | 0 failures |
| TC-02-03 | Unit tests with race detector | `go test -race ./internal/...` | All pass, no race warnings | 0 failures, 0 race |
| TC-02-04 | Integration tests pass | `go test ./test/integration/... -v -timeout 180s` | All 22 E2E tests pass | 0 failures |
| TC-02-05 | Reproducible build | `go build -trimpath ./cmd/server` | Binary produced | Strips local paths |

### TC-03 — Server Lifecycle

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-03-01 | Default flags | `./server` | Listens on `:2121`, root `/tmp/hybrid-ftp`, uses `users.conf` | Banner `[CTRL] server listening on :2121` |
| TC-03-02 | Custom `-ctrl` | `./server -ctrl :2222` | Listens on 2222 | Connection to 2222 returns 220 |
| TC-03-03 | Custom `-root` (non-existent) | `./server -root /tmp/does-not-exist` | Server auto-creates the directory | Directory created, server starts |
| TC-03-04 | Custom `-users` | `./server -users /tmp/other.conf` | Uses alternate file | Auth succeeds against that file |
| TC-03-05 | Port already in use | Run two servers with same `-ctrl :2121` | Second fails to bind | Second process exits non-zero with bind error in log |
| TC-03-06 | Missing `users.conf` | `./server -users /nonexistent.conf` | Server still starts; all `PASS` fail | First `PASS` returns 530, server logs `[AUTH] cannot open users file` |
| TC-03-07 | Malformed `users.conf` | Edit to `garbage_no_colon`, restart | All `PASS` fail | All `PASS` return 530, log shows no panic |
| TC-03-08 | Graceful shutdown (SIGINT) | Start server, `Ctrl+C` | Shutdown logged; existing sessions cancelled | Log shows `[CTRL] shutting down...` and `[CTRL] server stopped.` |
| TC-03-09 | Graceful shutdown (SIGTERM) | `kill -TERM <pid>` | Same as TC-03-08 | Same |
| TC-03-10 | Shutdown mid-transfer | Start 100 MB transfer, `Ctrl+C` server | Server terminates; client sees socket close | Transfer aborts within 1s, no zombie goroutine (check pprof / RSS stable) |

### TC-04 — Greeting & Banner

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-04-01 | 220 on connect | `nc 127.0.0.1 2121` | `220 Hybrid FTP Ready.` | Exact match |
| TC-04-02 | One banner per connection | Connect, send `NOOP`, `NOOP` | No additional 220; only 200 replies | Only one 220 in capture |

### TC-05 — Authentication Matrix

| ID | Objective | Pre-reqs | Steps | Expected | Pass criterion |
|----|-----------|----------|-------|----------|----------------|
| TC-05-01 | Happy path | — | `USER admin`, `PASS admin` | `331` then `230 Login successful.` | Both codes present |
| TC-05-02 | Wrong password | — | `USER admin`, `PASS wrong` | `530 Login failed.` | 530; log `[AUTH] user admin password mismatch` |
| TC-05-03 | Unknown user | — | `USER nobody`, `PASS anything` | `331` then `530` | Both codes; log `[AUTH] user nobody not found` |
| TC-05-04 | `USER` re-issue (re-arm) | Logged in as admin | `USER user1`, `PASS user` | `331`, then `230` for `user1` | Session re-armed; subsequent commands use `user1` |
| TC-05-05 | `PASS` without prior `USER` | Fresh connection | `PASS admin` | `530 Login failed.` | Treated as user `""` which is not in `users.conf` |
| TC-05-06 | Double `PASS` (already logged in) | Logged in | `PASS admin` again | `503 Already logged in.` | 503 reply |
| TC-05-07 | 4 failed login attempts | Fresh connection × 4 | `USER admin` + wrong `PASS` × 4 | All return 530 | No rate-limit / lock-out (per spec); account remains usable |
| TC-05-08 | Case-insensitive password hash match | Use mixed-case `PaSs` in `users.conf` | `PASS` with the same value | `230` | `auth.go` uses `strings.EqualFold` |
| TC-05-09 | Comments/blanks in `users.conf` | `users.conf` with `# comment\n\nuser:hash\n` | `PASS` succeeds | 230 | Lines with `#` prefix or empty are ignored |

> **B1 applies to TC-05-01/05-04**: the shipped `users.conf` is broken for `user1`. Either patch the file (change `8fb` → `8ee` on line 6) or use the admin account only. See Known Issues.

### TC-06 — Path Containment / Sandbox

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-06-01 | `CWD ..` from root | `CWD ..` | `550 Access denied.` or stays at root | Cannot escape |
| TC-06-02 | `CWD ../..` from root | `CWD ../..` | `550 Access denied.` | Cannot escape |
| TC-06-03 | `RETR /etc/passwd` (absolute) | `RETR /etc/passwd` | `550` | Absolute paths are resolved relative to root; file not present inside root |
| TC-06-04 | `RETR ../../../etc/passwd` | `RETR ../../../etc/passwd` | `550` | Path containment check rejects |
| TC-06-05 | `STOR /tmp/evil.txt` | `PASV`, `STOR /tmp/evil.txt` | `550 Access denied.` | Cannot write outside root |
| TC-06-06 | `MKD ../escape` | `MKD ../escape` | `550 Cannot create directory.` | No directory created outside root |
| TC-06-07 | `CWD /` (reset) | `CWD /`, `PWD` | `250` then `257 "/"` | PWD shows root |
| TC-06-08 | Symlink escape (POSIX) | `ln -sf /etc/passwd escape.lnk`, `RETR escape.lnk` | `550` or returns the symlink target only if it's inside root | Server uses `os.Open` (follows symlinks); confirm by inspecting the file delivered — it must be the symlink target, **not** a copy of `/etc/passwd` unless `/etc/passwd` is inside root |

### TC-07 — Navigation (PWD, CWD, CDUP)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-07-01 | PWD at root | Fresh login | `PWD` | `257 "/" is current directory.` |
| TC-07-02 | CWD into subdir | `CWD sub1` | `250 Directory changed.` | `PWD` shows `/sub1` |
| TC-07-03 | CDUP at root | Fresh login, `CDUP` | `250 Directory changed to parent.` | PWD still `"/"` (no-op) |
| TC-07-04 | CDUP from subdir | `CWD sub1`, `CDUP` | `250` | PWD back at root |
| TC-07-05 | CWD to missing dir | `CWD doesnotexist` | `550 Directory not found.` | Reply code 550 |
| TC-07-06 | CWD to a file (not dir) | Create `file.txt`, `CWD file.txt` | `550 Directory not found.` | `info.IsDir()` check |
| TC-07-07 | Deep nesting | `CWD sub1/sub2/sub3`, `PWD` | `250`, `257` shows full path | Path is `/sub1/sub2/sub3` |

### TC-08 — Directory Operations (MKD, RMD)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-08-01 | MKD happy path | `MKD newdir` | `257 "/newdir" created.` | Directory exists on disk |
| TC-08-02 | MKD no arg | `MKD` | `501 MKD requires a directory name.` | 501 |
| TC-08-03 | MKD duplicate | `MKD newdir`, `MKD newdir` | First 257, second 550 | Second fails |
| TC-08-04 | RMD empty dir | `MKD tmp`, `RMD tmp` | `257`, then `250 Directory removed.` | Directory removed from disk |
| TC-08-05 | RMD non-empty dir | `MKD tmp; touch tmp/x; RMD tmp` | `550 Cannot remove directory.` | `os.Remove` fails (not `RemoveAll`) |
| TC-08-06 | RMD no arg | `RMD` | `501 RMD requires a directory name.` | 501 |

### TC-09 — Listing (LIST, NLST, STAT)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-09-01 | LIST root with files | Create `a.txt`, `b.txt`, `somedir/`; `PASV`, `LIST` | `150` then directory listing contains `a.txt`, `b.txt`, `somedir/` | All three present in the data payload |
| TC-09-02 | LIST subdir | `PASV`, `LIST sub1` | Listing of `sub1/` | Correct contents |
| TC-09-03 | LIST missing path | `LIST nope` | `550 Cannot list directory.` | 550 |
| TC-09-04 | LIST without data mode | `LIST` (no PASV/PORT) | `503 Use PASV or PORT first.` | 503 |
| TC-09-05 | NLST happy | `PASV`, `NLST` | Names only (no metadata) one per line | `a.txt\r\nb.txt\r\n...` |
| TC-09-06 | NLST of empty dir | `MKD empty; PASV; NLST empty` | Empty payload + `226` | 226 with no names |
| TC-09-07 | STAT (no arg) | `STAT` | `211 Hybrid FTP server status: running.` | 211 |
| TC-09-08 | STAT on file | `STAT hello.txt` | `213 File: hello.txt \| Size: N bytes \| ...` | 213, contains `Size:` |
| TC-09-09 | STAT on dir | `STAT sub1` | `212 Directory: sub1 \| ...` | 212 |
| TC-09-10 | STAT missing | `STAT nope` | `550 Cannot stat path.` | 550 |

### TC-10 — File Metadata (SIZE, MDTM)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-10-01 | SIZE file | Create file with `12345` bytes; `SIZE f` | `213 5` | 213 + exact byte count |
| TC-10-02 | SIZE dir | `SIZE sub1` | `550 Cannot get file size.` | 550 |
| TC-10-03 | SIZE missing | `SIZE nope` | `550` | 550 |
| TC-10-04 | SIZE no arg | `SIZE` | `501 SIZE requires a filename.` | 501 |
| TC-10-05 | MDTM file | `MDTM hello.txt` | `213 YYYYMMDDhhmmss` | 213, 14 digits |
| TC-10-06 | MDTM no arg | `MDTM` | `501 MDTM requires a filename.` | 501 |

### TC-11 — Transfer Setup (TYPE, MODE)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-11-01 | TYPE A | `TYPE A` | `200 Type set.` | 200 |
| TC-11-02 | TYPE I | `TYPE I` | `200 Type set.` | 200 |
| TC-11-03 | TYPE X (invalid) | `TYPE X` | `500 TYPE must be A or I.` | 500 |
| TC-11-04 | TYPE no arg | `TYPE` | `500 TYPE must be A or I.` | 500 |
| TC-11-05 | MODE S | `MODE S` | `200 Mode set to Stream.` | 200 |
| TC-11-06 | MODE B | `MODE B` | `504 Block mode not implemented.` | 504 (expected per `AGENTS.md`) |
| TC-11-07 | MODE C | `MODE C` | `504 Compressed mode not implemented.` | 504 (expected) |
| TC-11-08 | MODE X (invalid) | `MODE X` | `501 MODE must be S, B, or C.` | 501 |
| TC-11-09 | MODE no arg | `MODE` | `501 MODE requires S, B, or C.` | 501 |

### TC-12 — Active vs Passive Mode (PORT, PASV)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-12-01 | PASV reply format | `PASV` | `227 Entering Passive Mode (h1,h2,h3,h4,p1,p2).` | 6 numbers, all 0–255 |
| TC-12-02 | PASV port encoding | `PASV`, parse port | `port = p1*256 + p2` | Computed port equals UDP `LocalAddr().Port` |
| TC-12-03 | PASV server IP matches bound interface | Server bound on specific IP, `PASV` | h1..h4 = bound IP | Server uses `conn.LocalAddr()` (per code) |
| TC-12-04 | PORT parse — 5 numbers | `PORT 1,2,3,4,5` | `501 PORT requires h1,h2,h3,h4,p1,p2.` | 501 |
| TC-12-05 | PORT parse — 256+ | `PORT 1,2,3,4,1,300` | `501 Invalid PORT parameter.` | 501 |
| TC-12-06 | PORT parse — non-numeric | `PORT a,b,c,d,e,f` | `501 Invalid PORT parameter.` | 501 |
| TC-12-07 | PORT then RETR | `PORT 127,0,0,1,200,0; RETR hello.txt` | 200, 150, 226 | Transfer completes |
| TC-12-08 | PORT then STOR | `PORT …; STOR upload.txt` | 200, 150, 226 | Upload completes |
| TC-12-09 | PASV → PORT switch | `PASV; PORT …` | 227 then 200 | Mode flips, next data op uses PORT |
| TC-12-10 | PORT → PASV switch | `PORT …; PASV` | 200 then 227 | Mode flips |
| TC-12-11 | PASV is single-use (B2) | `PASV; RETR a; RETR b` **without re-PASV** | First 226; second hangs / times out | This is the known bug; second op requires a fresh `PASV` |
| TC-12-12 | PORT is single-use | `PORT; RETR a; RETR b` without re-PORT | First 226; second errors with `no PORT socket` | Documented behavior in README §9 |
| TC-12-13 | LIST/RETR/STOR without mode | Logged in fresh, `RETR hello.txt` | `503 Use PASV or PORT first.` | 503 |
| TC-12-14 | PORT on IPv6 client | Connect via IPv6, send `PORT …` | Should work (server parses 4-byte IPv4) or fail with 501 | Per code, IPv4 only |

### TC-13 — RETR (Download)

| ID | Objective | Pre-reqs | Steps | Expected | Pass criterion |
|----|-----------|----------|-------|----------|----------------|
| TC-13-01 | Small ASCII file | `hello.txt` ~21 B | `PASV; RETR hello.txt` | 150, payload 21 B, 226 | Bytes match original |
| TC-13-02 | Large binary file (50 MB) | `big50.bin` 50 MB | `PASV; RETR big50.bin` | 150, progress bar shows updates, 226 | Size + SHA-256 match |
| TC-13-03 | Zero-byte file | `touch empty.bin; PASV; RETR empty.bin` | 150, payload 0 B, 226 | `SIZE empty.bin` returns 0 |
| TC-13-04 | Multi-window file | `big100.bin` 100 MB | `PASV; RETR big100.bin` | 150, 226 | No data loss across window |
| TC-13-05 | CRLF preservation | Create `crlf.txt` with `\r\n` line endings | `TYPE I; PASV; RETR crlf.txt` | Bytes unchanged (binary mode) | `xxd crlf.txt` matches server `xxd` |
| TC-13-06 | Missing file | `PASV; RETR nope` | `550 Cannot open nope.` | 550; data connection cleaned up |
| TC-13-07 | No data mode | `RETR hello.txt` (no PASV) | `503` | 503 |

### TC-14 — STOR (Upload)

| ID | Objective | Pre-reqs | Steps | Expected | Pass criterion |
|----|-----------|----------|-------|----------|----------------|
| TC-14-01 | Small upload | `upload.txt` (local) | `PASV; STOR upload.txt` | 150, progress bar, 226 | Server file byte-matches local |
| TC-14-02 | Large binary upload (50 MB) | Local 50 MB | `PASV; STOR big50_up.bin` | 150, 226 | Server file size + SHA-256 match |
| TC-14-03 | Overwrite existing | `STOR upload.txt` twice | Both 150, 226 | Server file replaced; new SHA = local |
| TC-14-04 | Missing local file | `STOR nope.txt` (no local file) | Client prints `Cannot open local file:` | Client does not send STOR |
| TC-14-05 | No data mode | `STOR upload.txt` (no PASV) | Client prints `Use PASV or PORT first.` | No STOR sent |
| TC-14-06 | Upload into subdir | `CWD sub1; PASV; STOR new.bin` | 257/150/226 | File appears at `<root>/sub1/new.bin` |

### TC-15 — STOU (Store Unique)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-15-01 | Unique name in 257 | `PASV; STOU` then `Send data` | `150` opening; `257 "/stou_<ts>_<hex>.dat" stored.` | Reply includes the server-generated name |
| TC-15-02 | Two STOU differ | `STOU` then `STOU` | Two different `stou_…` names | Names differ |
| TC-15-03 | Byte match | `STOU` with payload `unique` | Server file contents == `unique` | SHA-256 of server file == local payload |

### TC-16 — APPE (Append)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-16-01 | Append to existing | `echo hello > a.txt; PASV; APPE a.txt` (send ` world`) | 150, 226 | Server `a.txt` == `hello world` |
| TC-16-02 | Append to non-existent (creates) | `PASV; APPE new.txt` (send `data`) | 150, 226 | Server `new.txt` == `data` |
| TC-16-03 | Multi-append | Three APPE calls | 226 each | Final content == concatenation |

### TC-17 — DELE (Delete)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-17-01 | Delete file | `DELE hello.txt` | `250 File deleted.` | File removed from disk |
| TC-17-02 | Delete missing | `DELE nope` | `550 Cannot delete file.` | 550 |
| TC-17-03 | Delete directory | `MKD x; DELE x` | `550 Cannot delete file.` | 550 (server uses `os.Remove` not `os.RemoveAll`) |
| TC-17-04 | Delete with `..` | `DELE ../escape` | `550 Access denied.` | 550 |
| TC-17-05 | Delete no arg | `DELE` | `501 DELE requires a filename.` | 501 |

### TC-18 — RNFR / RNTO

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-18-01 | Rename file | `RNFR old; RNTO new` | `350`, `250 Rename successful.` | `new` exists, `old` does not |
| TC-18-02 | Rename over existing | `RNFR a; RNTO b` where b exists | `350`, `250` | `b` content replaced (per `os.Rename` semantics) |
| TC-18-03 | RNTO without prior RNFR | `RNTO new` | `503 RNFR required first.` | 503 |
| TC-18-04 | RNTO no arg | `RNFR a; RNTO` | `501 RNTO requires a filename.` | 501 |
| TC-18-05 | RNFR non-existent | `RNFR nope` | `550 File not found.` | 550 |
| TC-18-06 | RNFR with `..` | `RNFR ../escape` | `550 File not found.` (or `Access denied`) | 550 |
| TC-18-07 | RNTO after reconnect | `RNFR a; QUIT; new conn; RNTO b` | RNFR state lost across sessions → `503` | Per-session state; no persistence |
| TC-18-08 | Sequential renames | `RNFR a; RNTO b; RNFR b; RNTO c` | All 250 | RNFR/RNTO pair resets each cycle |

### TC-19 — HASH (Integrity Check)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-19-01 | ASCII file | `HASH hello.txt` | `213 SHA-256 0-hello.txt 5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03` | Hex matches `sha256sum` |
| TC-19-02 | Empty file | `HASH empty.bin` | `213 SHA-256 0-empty.bin <e3b0…>` | Hash == `sha256sum` of empty file |
| TC-19-03 | Binary file | `HASH big50.bin` | `213 SHA-256 0-big50.bin <hex>` | Matches `sha256sum big50.bin` |
| TC-19-04 | Missing file | `HASH nope` | `550 Cannot compute hash.` | 550 |
| TC-19-05 | No arg | `HASH` | `501 HASH requires a filename.` | 501 |
| TC-19-06 | Directory | `HASH sub1` | `550 Cannot compute hash.` | 550 |
| TC-19-07 | Client-side match check | After `HASH`, the client CLI must print `Hashes match - file integrity verified.` for equal hashes and `MISMATCH!` for mismatches | Exactly one of those two lines | Behavior of `cmd/client/main.go:handleHASH` |

### TC-20 — ABOR / NOOP / HELP

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-20-01 | NOOP | `NOOP` | `200 NOOP ok.` | 200 |
| TC-20-02 | ABOR with no transfer | `ABOR` | `226 ABOR successful.` | 226 |
| TC-20-03 | ABOR mid-transfer (large file) | Start 100 MB RETR, send `ABOR` after 200 ms | `426 Transfer aborted.` | Server log shows `[CTRL] transfer aborted by ABOR` |
| TC-20-04 | HELP (no arg) | `HELP` | `214 Supported commands: <list>` | Reply starts with 214 |
| TC-20-05 | HELP with arg | `HELP RETR` | `214 Help for RETR: see RFC 959.` | Generic per-command text (per code) |
| TC-20-06 | ABOR then re-PASV | `ABOR; PASV; RETR a` | 226, 227, 150, 226 | Session recoverable after abort |

### TC-21 — Local Client Commands (lcd/lls/lpwd)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-21-01 | lpwd | `lpwd` | Prints absolute cwd | Match `pwd`/`Get-Location` |
| TC-21-02 | lcd existing | `lcd /tmp` | No error output | Next `lpwd` shows `/tmp` |
| TC-21-03 | lcd missing | `lcd /no/such/dir` | Prints `lcd: …` error; session continues | `ftp> ` prompt returns |
| TC-21-04 | lcd no arg | `lcd` | `os.Chdir("")` error printed; session continues | No crash |
| TC-21-05 | lls | `lls` | Lists cwd with `name` or `name  NNN` format | Visible output, no crash |
| TC-21-06 | lls after lcd | `lcd /tmp; lls` | Reflects new cwd | Directory contents differ from initial |
| TC-21-07 | Lowercase | `lcd` / `lls` / `lpwd` (already lowercase) | Same as above | Case-insensitive in client parser (per `strings.HasPrefix(upper, ...)`) |

### TC-22 — RDT Protocol Verification (Lower Layer)

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-22-01 | 3-way handshake log | Start a transfer, `grep "\[RDT\]" server.log` | See SYN → SYN+ACK → ACK exchange | At least one SYN+ACK line in log |
| TC-22-02 | Per-packet ACK | Same as TC-22-01 | Log shows periodic `sender recv loop` activity | ACKs observed |
| TC-22-03 | Packet loss induction (Linux) | `iptables -A INPUT -p udp --sport <PASV-port> -m statistic --mode random --probability 0.05 -j DROP` then `RETR big50.bin` | Transfer still completes (retransmits) | `grep "retransmitting seq=" server.log` shows ≥1 line; final SHA matches |
| TC-22-04 | Packet loss induction (Windows) | Use [clumsy](https://jagt.github.io/clumsy/) on the UDP socket; or `netsh` filtering | Same as TC-22-03 | Same |
| TC-22-05 | Checksum rejection | Inject a single flipped byte via raw socket (or via `tc qdisc` netem) | Transfer completes via retransmit | Log shows checksum-related handling |
| TC-22-06 | Reordering | Use `tc qdisc add dev lo root netem delay 10ms reorder 25%` | Transfer completes | Bytes match |
| TC-22-07 | 3-retransmit cap → RST | Drop **all** packets to PASV port for >30 s | Server logs `[RDT] max retransmits exceeded` and `RST` | Sender aborts with error, server returns 426 |
| TC-22-08 | MSS chunking | `RETR` 50 MB file; `tcpdump`/`Wireshark` UDP packets | Each packet ≤ 1421 bytes (1400 payload + 21 header) | No packet > 1421 bytes |
| TC-22-09 | Sliding window | Induce loss to produce out-of-order arrivals; observe `buffered out-of-order seq=…` log lines | Out-of-order buffering observed | Log contains at least one such line during lossy run |

> Tools: `tcpdump -i lo -nn -X udp port <p>`, `wireshark`, `iptables`, `tc qdisc` (Linux), `clumsy` (Windows).

### TC-23 — BBR State Verification

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-23-01 | STARTUP → DRAIN | Transfer big file; `grep "BBR: STARTUP -> DRAIN" server.log` | Transition occurs | Line present within first ~3 RTTs |
| TC-23-02 | DRAIN → PROBE_BW | Same transfer | Transition occurs | Log shows `BBR: DRAIN -> PROBE_BW` |
| TC-23-03 | PROBE_BW → PROBE_RTT (10 s) | Run transfer ≥ 12 s | Transition occurs after ~10 s | Log shows `BBR: PROBE_BW -> PROBE_RTT` |
| TC-23-04 | PROBE_RTT → PROBE_BW (200 ms) | After TC-23-03 | Transition occurs | Log shows `BBR: PROBE_RTT -> PROBE_BW` |
| TC-23-05 | cwnd floor (4·MSS) | At transfer start, before any ACK, sender inflight cap = 5600 bytes | Cannot send > 5600 in flight | Inspect via debug log or unit test `TestBBR_OnSendAndCanSend` |

> Note: PROBE_RTT only fires once per ~10 s. To exercise it deterministically, run a single continuous transfer > 15 s.

### TC-24 — Concurrency & Performance

| ID | Objective | Steps | Expected | Pass criterion |
|----|-----------|-------|----------|----------------|
| TC-24-01 | 2 parallel sessions, independent PASV | 2 clients, each `PASV; RETR big50.bin` | Both 226 | Each session has unique `[SESS] registered session N`; transfers independent |
| TC-24-02 | 5 parallel RETR (stress) | 5 concurrent `RETR big50.bin` from 5 clients | All 5 complete | All 5 clients receive 226; server log shows ≥ 5 distinct session IDs |
| TC-24-03 | Single 500 MB transfer | `dd if=/dev/urandom of=big500.bin bs=1M count=500; PASV; RETR big500.bin` | 150, 226 | Size + SHA-256 match |
| TC-24-04 | Progress bar cadence | Watch stdout during large RETR | Updates every ≤ 100 ms | At least 10 update lines per second |
| TC-24-05 | Post-transfer integrity | After TC-24-03, `HASH big500.bin` (client) | `Hashes match - file integrity verified.` | Exact line printed |
| TC-24-06 | Session isolation | Client A `CWD sub1; STOR a.txt` while Client B `CWD sub2; RETR b.txt` | No cross-talk | CWD changes do not affect other client |
| TC-24-07 | Resource cleanup | After TC-24-02, server still responsive; no leaked goroutines (use `runtime.NumGoroutine` before/after) | Goroutine count returns to baseline within 5 s | Δ goroutines ≈ 0 |

---

## Cross-Cutting Tables

### A. Command × Reply-Code Matrix

| Command | 1xx | 2xx | 3xx | 4xx | 5xx | Notes |
|---------|-----|-----|-----|-----|-----|-------|
| `USER` | — | — | `331` | — | `501` (missing name) | Sets pending username |
| `PASS` | — | `230` | — | `503` (already) | `530` | SHA-256 verified |
| `QUIT` | — | `221` | — | — | — | Terminates session |
| `NOOP` | — | `200` | — | — | — | Keep-alive |
| `PWD` | — | `257` | — | — | — | Includes absolute path |
| `CWD` | — | `250` | — | — | `550` | Path must be directory |
| `CDUP` | — | `250` | — | — | — | No-op at root |
| `MKD` | — | `257` | — | — | `550`, `501` | 501 on missing name |
| `RMD` | — | `250` | — | — | `550`, `501` | Only empty dirs |
| `LIST` | `150` | `226` | — | `425`, `426` | `550`, `503` | Requires data mode |
| `NLST` | `150` | `226` | — | `425`, `426` | `550`, `503` | Names only |
| `STAT` | — | `211`/`212`/`213` | — | — | `550` | No data channel |
| `SIZE` | — | `213` | — | — | `550`, `501` | Bytes |
| `MDTM` | — | `213` | — | — | `550`, `501` | `YYYYMMDDhhmmss` |
| `TYPE` | — | `200` | — | — | `500` | A or I only |
| `MODE` | — | `200` | — | — | `501`, `504` | S=200, B/C=504 |
| `PORT` | — | `200` | — | — | `501` | IPv4 only |
| `PASV` | — | `227` | — | — | `425` | Single-use |
| `RETR` | `150` | `226` | — | `425`, `426` | `550`, `503` | Requires data mode |
| `STOR` | `150` | `226` | — | `425`, `426` | `550`, `503` | Requires data mode |
| `STOU` | `150` | `257` | — | `425`, `426` | `550`, `503` | Unique name in 257 |
| `APPE` | `150` | `226` | — | `425`, `426` | `550`, `503` | Creates if absent |
| `DELE` | — | `250` | — | — | `550`, `501` | Files only |
| `RNFR` | — | — | `350` | — | `550`, `501` | Pending rename |
| `RNTO` | — | `250` | — | `503` | `550`, `501` | Completes rename |
| `HASH` | — | `213` | — | — | `550`, `501` | SHA-256 hex |
| `ABOR` | — | `226` | — | `426` | — | 426 only mid-transfer |
| `HELP` | — | `214` | — | — | — | 214 always |
| unknown | — | — | — | — | `500` | "Unknown command." |

### B. RDT Flag Combinations

| Direction | Flag combo | Trigger | Where seen |
|-----------|-----------|---------|------------|
| C → S | `SYN` | `Connect()` | `sender.go:sendControl` |
| S → C | `SYN\|ACK` | `Accept()` / reply to client `Connect()` | `receiver.go:sendControl` |
| C → S | `ACK` | 3-way handshake final | `sender.go:Accept` |
| C → S | `DATA` | `senderLoop` | `sender.go:sendLoop` |
| C → S | `FIN` | `Close()` | `sender.go:Close` |
| S → C | `FIN\|ACK` | `Close()` after FIN received | `receiver.go:Close` |
| Either | `RST` | Max-retransmit exceeded | `sender.go:onTimeout`, `receiver.go:receiveLoop` |

### C. BBR State × Pacing Gain × Trigger

| State | pacingGain | Trigger to enter | Trigger to exit | Log marker |
|-------|------------|------------------|-----------------|-------------|
| `bbrStartup` | 2.89 | `NewBBR()` | 3 consecutive RTTs with < 25 % bandwidth growth | `BBR: STARTUP -> DRAIN` |
| `bbrDrain` | 0.75 | Exit from STARTUP | `inflight ≤ BDP` | `BBR: DRAIN -> PROBE_BW` |
| `bbrProbeBW` | cyclic 1.25 / 0.75 / 1.0 (8 phases) | Exit from DRAIN | 10 s elapsed | `BBR: PROBE_BW -> PROBE_RTT` |
| `bbrProbeRTT` | n/a (cwnd = 4·MSS) | 10 s since last probe | 200 ms dwell | `BBR: PROBE_RTT -> PROBE_BW` |

### D. Known Issues Found During Review

| ID | Severity | Description | Evidence | Suggested fix |
|----|----------|-------------|----------|----------------|
| **B1** | High | `users.conf` line 6 has a typo: hash ends in `8fb` instead of `8ee`. Fresh `user1:user` login against the shipped file returns `530`. All other artifacts (README, `docs/ARCHITECTURE.md`, both `*_design.md` and `*_implementation.md` specs, `internal/auth/auth_test.go`, `test/integration/e2e_test.go`) use the correct hash. | `users.conf:6` | Change `…8fb` → `…8ee` on line 6 |
| **B2** | High | README claims `PASV` "auto-resends before subsequent data commands", but the client never does. After the first `PASV`-mode transfer, the server closes the UDP socket in `cleanupDataConn`, and the client's `ensurePASV()` returns early because `currentPASVHost != ""`. The second data command hangs until timeout. | `cmd/client/main.go:200-218` (`ensurePASV`), `cmd/server/handlers.go:251` (`cleanupDataConn`) | Either (a) re-issue `PASV` in `ensurePASV` whenever `modePASV` and no in-flight socket, or (b) keep the server's PASV socket open for the session lifetime. Recommended: (b). |
| **L1** | Doc | `HELP <command>` is a one-liner "see RFC 959" for every command, not a per-command summary. | `cmd/server/handlers.go:handleHELP` | Optional: add a per-command help table. |
| **L2** | Doc | `TYPE A` vs `TYPE I` is stored on the session but never enforced on the data path — bytes pass through unchanged. | `cmd/server/handlers.go:handleTYPE` (stores `sess.TransferType` but no transformation) | Optional: implement `\r\n` ↔ `\n` conversion in TYPE A. |
| **L3** | Doc | `MODE B` and `MODE C` return `504` per `AGENTS.md` (deferred). | `cmd/server/handlers.go:handleMODE` | Documented; not a defect. |
| **L4** | Doc | `PORT` only supports IPv4; IPv6 client connection falls through to `To4()` returning `nil` and the client prints "PORT: IPv6 not supported". | `cmd/client/main.go:handlePORT` | Optional: add `EPRT`. |
| **L5** | Doc | `NLST` output is just names joined with `\r\n`. The format follows RFC 959 loosely. | `cmd/server/handlers.go:handleNLST` | Optional: format with size + perms. |
| **L6** | Doc | `STAT <path>` format is custom (`File: … \| Size: …`). | `cmd/server/handlers.go:handleSTAT` | Optional: align with RFC 959 §4.1.3. |
| **L7** | Doc | No `FEAT` command (RFC 2389). | n/a | Optional: add `FEAT` listing 28 implemented commands. |
| **L8** | Doc | No TLS / encryption. | n/a | Out of scope per `AGENTS.md` ("NO external libraries"). |
| **L9** | Doc | No resumable transfers (`REST` / `APPE`-partial). `APPE` exists but `REST` does not. | n/a | Optional. |
| **L10** | Doc | Single data connection per transfer. | `cleanupDataConn` after every data op | Same as B2; intentional single-use design. |

### E. Demo Evidence Checklist

Per `REQUIREMENTS.md §3.7` (Application Demo Evidence), capture:

- [ ] Screenshot/log of **login** (USER + 331, PASS + 230).
- [ ] Screenshot/log of **ASCII download** (small file, complete).
- [ ] Screenshot/log of **binary download** (`big50.bin`, complete with progress bar).
- [ ] Screenshot/log of **upload** (STOR `big50.bin`, 226).
- [ ] Screenshot/log of **hash match** (`HASH big50.bin` then `Hashes match - file integrity verified.`).
- [ ] Screenshot/log of **hash mismatch** (modify file locally, `HASH` again → `MISMATCH!`).
- [ ] Screenshot/log of **directory navigation** (`CWD`, `CDUP`, `MKD`, `RMD`, `LIST`).
- [ ] Screenshot/log of **active mode** (PORT, RETR, 226).
- [ ] Screenshot/log of **passive mode** (PASV, STOR, 226).
- [ ] Screenshot/log of **concurrent sessions** (2 clients side-by-side).
- [ ] Screenshot of **server log** showing `[CTRL]`, `[RDT]`, `[FS]`, `[SESS]`, `[AUTH]` prefixes and BBR state transitions.
- [ ] Screenshot/log of **ABOR mid-transfer** (large file → `426 Transfer aborted.`).
- [ ] Wireshark capture of the UDP data channel during one transfer (SYN → DATA → ACK → FIN).

### F. Test Report Template

```markdown
## Manual Test Run Report

- **Date**: YYYY-MM-DD
- **Tester**:
- **Build commit**:
- **Server flags**: `-ctrl :2121 -root <path> -users users.conf`
- **OS / Go version**:
- **TC-01 to TC-21 summary**: __ passed / __ failed / __ blocked
- **TC-22 RDT verification**: __ passed / __ failed
- **TC-23 BBR verification**: __ passed / __ failed
- **TC-24 Concurrency**: __ passed / __ failed
- **Defects filed** (link to issues):
- **Screenshots / logs attached**:
- **Notes**:
```

---

## Project Structure

```
hybrid-ftp/
├── cmd/
│   ├── server/main.go       # Server entry point
│   └── client/main.go       # Client CLI entry point
├── internal/
│   ├── auth/auth.go          # SHA-256 credential verification
│   ├── fs/ops.go             # File system operations
│   ├── rdt/
│   │   ├── bbr.go            # BBR congestion control
│   │   ├── checksum.go       # CRC32-IEEE computation
│   │   ├── header.go         # 21-byte packet encode/decode
│   │   ├── progress.go       # ANSI terminal progress bar
│   │   ├── receiver.go       # RDT receiver (sliding window)
│   │   ├── sender.go         # RDT sender (Selective Repeat)
│   │   └── timer.go          # Per-packet retransmit timers
│   ├── session/manager.go    # Session registry + lifecycle
│   └── tcpctrl/
│       ├── commands.go       # FTP command parser (25 commands)
│       ├── replies.go        # Reply code constants + formatting
│       └── protocol.go       # TCP connection handler (shared)
├── test/
│   ├── integration/e2e_test.go  # End-to-end integration tests
│   └── fixtures/sample.txt
├── users.conf                # SHA-256 hashed user credentials
├── go.mod
└── AGENTS.md                 # Developer reference + conventions
```
