# Implementation Task Checklist — Hybrid FTP

> **Legend**: `[A]` = Developer A (Server), `[B]` = Developer B (Client), `[Both]` = Shared responsibility.

---

## Phase 1: TCP Control Plane & CLI/Server Skeletons

### 1.1 Project Bootstrap
- [x] `[A]` Initialize Go module (`go mod init hybrid-ftp`)
- [x] `[Both]` Create directory structure per `AGENTS.md`
- [x] `[Both]` Write `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/TASKS.md`, `docs/GENAI_LOG.md`

### 1.2 Authentication Module (`internal/auth/`)
- [ ] `[A]` Implement `auth.go`: `CheckPassword(username, password string) bool`
  - Read `users.conf`; SHA-256 hash the provided password; compare to stored hash
  - Log with `[AUTH]` prefix
- [ ] `[A]` Unit tests: `auth_test.go` — valid login, wrong password, nonexistent user

### 1.3 Control Protocol — Shared Types (`internal/tcpctrl/`)
- [ ] `[A]` Implement `commands.go`: Command enum (all 25 commands), `ParseCommand(line string) (Cmd, args string, err error)`
- [ ] `[A]` Implement `replies.go`: Reply code constants (all from spec), `FormatReply(code int, msg string) string` — returns `"code msg\r\n"`
- [ ] `[A]` Implement `protocol.go`: `ReadCommand(conn)`, `WriteReply(conn, code, msg)`
- [ ] `[A]` Unit tests: `replies_test.go`, `commands_test.go` — all commands parse correctly, replies formatted properly

### 1.4 Session Manager (`internal/session/`)
- [ ] `[A]` Implement `manager.go`:
  - `Session` struct: `ID uint32`, `conn net.Conn`, `username string`, `authenticated bool`, `cwd string`, `dataMode string (PORT/PASV)`, `dataAddr *net.UDPAddr`, `transferType byte (A/I)`, `rnfrPath string`, `ctx context.Context`, `cancel context.CancelFunc`
  - `SessionManager` struct: `registry map[uint32]*Session` + `sync.RWMutex`, `nextID uint32`
  - Methods: `Register(conn) *Session`, `Unregister(id)`, `Get(id) *Session`, `List() []*Session`
  - Log with `[SESS]` prefix
- [ ] `[A]` Unit tests: `manager_test.go` — register, get, unregister, concurrent access

### 1.5 Server Main (`cmd/server/main.go`)
- [ ] `[A]` Implement `main.go`:
  - Parse flags (port, data-port, root, users-file)
  - Create `SessionManager`
  - Listen TCP on `:port`
  - Accept loop: spawn `handleSession()` goroutine per connection
  - Signal handling for graceful shutdown (SIGINT/SIGTERM)
  - Log with `[CTRL]` prefix

### 1.6 Client CLI (`cmd/client/main.go`)
- [ ] `[B]` Implement `main.go`:
  - Parse flags (host, port)
  - Connect TCP to server
  - Read server greeting (220)
  - CLI read-eval-print loop: read user input, send to server, print reply
  - Handle local commands: `lcd`, `lls`, `lpwd`
  - On exit: send QUIT

### 1.7 Control Command Dispatch (Server-side)
- [ ] `[A]` In `handleSession()`: implement command dispatch loop
  - Read line → parse command → switch on command enum → call handler → send reply
  - Skip dispatch for unimplemented auth-gated commands if not authenticated → `530`
  - Stub handlers for all commands (return `200 Command OK` placeholder)
  - Handle QUIT: `221 Goodbye.` → close connection

---

## Phase 2: File/Directory Operations & Advanced Commands

### 2.1 File System Operations (`internal/fs/ops.go`)
- [ ] `[A]` Implement directory operations:
  - `PWD(session) string` → return session.cwd
  - `CWD(session, path string) error` → resolve path relative to cwd, validate it exists and is a directory, update cwd
  - `CDUP(session) error` → CWD to `..`
  - `MKD(session, dirname string) error` → create directory
  - `RMD(session, dirname string) error` → remove empty directory
- [ ] `[A]` Implement listing operations:
  - `LIST(session, path string) ([]FileInfo, error)` → detailed listing with name, size, type, permissions
  - `NLST(session, path string) ([]string, error)` → name-only listing
  - `STAT(session, path string) (string, error)` → file/directory metadata or server status
  - `SIZE(session, filename string) (int64, error)` → exact file size in bytes
  - `MDTM(session, filename string) (time.Time, error)` → last modification timestamp
- [ ] `[A]` Implement file transfer operations (server-side preparation):
  - `OpenForRead(session, filename string) (*os.File, os.FileInfo, error)` → open file for RETR
  - `OpenForWrite(session, filename string) (*os.File, error)` → open/create file for STOR
  - `OpenForAppend(session, filename string) (*os.File, error)` → open for APPE
  - `DELE(session, filename string) error` → delete file
  - `RNFR(session, oldname string) error` → check file exists, store path in session
  - `RNTO(session, newname string) error` → rename from stored RNFR path
- [ ] `[A]` Implement STOU: generate `UUID_timestamp_originalname`, create file
- [ ] `[A]` Unit tests: `ops_test.go` — all file operations within the server root

### 2.2 HASH Command (`internal/fs/ops.go`)
- [ ] `[A]` Implement `ComputeHash(filename string) (string, error)`:
  - Read file in 64KB chunks, feed to `sha256.New()`
  - Return hex-encoded digest
- [ ] `[B]` Implement client-side `ComputeHash(filename string) (string, error)` (identical logic)
- [ ] `[Both]` Hook HASH into control dispatch: server returns `213 <hexhash>`, client compares local vs remote

### 2.3 TYPE and MODE Commands
- [ ] `[A]` TYPE: store `transferType` in session (`A` or `I`). For Phase 2, ASCII and Binary are treated identically (raw bytes).
- [ ] `[A]` MODE S: supported. MODE B, MODE C: return `504 Not implemented.`

### 2.4 Data Channel Setup — PORT and PASV
- [ ] `[A]` PORT command handler:
  - Parse `h1,h2,h3,h4,p1,p2` → `net.UDPAddr`
  - Store in session: `dataMode = "PORT"`, `dataAddr = parsed`
  - Return `200 PORT command successful.`
- [ ] `[A]` PASV command handler:
  - Server binds a UDP socket to `0.0.0.0:0` (random port)
  - Retrieve assigned port
  - Store socket reference in session
  - Reply: `227 Entering Passive Mode (h1,h2,h3,h4,p1,p2)` — use server's external IP
- [ ] `[B]` Client-side PORT/PASV command builders:
  - PORT: accept local IP and port from user, format as `h1,h2,h3,h4,p1,p2`
  - PASV: parse 227 response, extract IP and port, prepare local UDP socket accordingly

### 2.5 Client-Side Command Implementations
- [ ] `[B]` Build command formatting for all commands (USER, PASS, PWD, CWD, etc.) in `cmd/client/main.go`
- [ ] `[B]` Implement `lcd`, `lls`, `lpwd` local commands
- [ ] `[B]` Implement LOCAL → REMOTE file path mapping for STOR/RETR

---

## Phase 3: Custom Reliable UDP (RDT) Layer & BBR Congestion Control

### 3.1 RDT Packet Header (`internal/rdt/header.go`)
- [ ] `[B]` Implement `RDTHeader` struct (21 bytes per spec)
- [ ] `[B]` Implement `Encode(h *RDTHeader, payload []byte) []byte` — serialize to wire format
- [ ] `[B]` Implement `Decode(data []byte) (*RDTHeader, []byte, error)` — deserialize, validate PayloadLen
- [ ] `[B]` Flag constants: `FlagSYN`, `FlagFIN`, `FlagACK`, `FlagRST`, `FlagDATA`
- [ ] `[B]` Unit tests: `header_test.go` — encode/decode round-trip, all flag combinations, edge payload lengths

### 3.2 CRC32 Checksum (`internal/rdt/checksum.go`)
- [ ] `[B]` Implement `ComputeChecksum(data []byte) uint32` — CRC32-IEEE
- [ ] `[B]` Implement `VerifyChecksum(data []byte) bool` — compute and compare
- [ ] `[B]` Unit tests: `checksum_test.go` — known vectors, corruption detection

### 3.3 RDT Receiver (`internal/rdt/receiver.go`)
- [ ] `[B]` Implement `RDTReceiver` struct:
  - `conn *net.UDPConn`, `sessionID uint32`
  - `state` enum: CLOSED, LISTENING, ESTABLISHED, RECEIVING, LAST_ACK
  - `rcvBase uint32` — next expected in-order sequence number
  - `recvBuffer map[uint32][]byte` — out-of-order packet buffer (ring or map with window limit)
  - `recvWindow uint16` — advertised window size (rwnd)
- [ ] `[B]` Implement receiver methods:
  - `Listen(addr *net.UDPAddr) error` — bind UDP socket
  - `Accept() error` — 3-way handshake: rcv SYN → send SYN+ACK → rcv ACK
  - `Receive() ([]byte, error)` — receive data loop:
    1. Read UDP datagram
    2. Verify checksum → discard on fail
    3. Parse header → dispatch by flags
    4. Handle DATA: check order, buffer, deliver to application, send ACK
    5. Handle FIN: send FIN+ACK, transition to LAST_ACK
  - `Close() error` — graceful shutdown
- [ ] `[B]` Unit tests: `receiver_test.go` — in-order delivery, out-of-order buffering, duplicate detection, checksum corruption discard

### 3.4 Per-Packet Timers (`internal/rdt/timer.go`)
- [ ] `[B]` Implement `PacketTimer` struct:
  - `timers map[uint32]*time.Timer` — one timer per unacked packet
  - `timeout time.Duration`
  - `backoff map[uint32]int` — exponential backoff per packet
  - Callback `OnTimeout(seq uint32)` — notifies sender to retransmit
- [ ] `[B]` Methods:
  - `Start(seq uint32)` — start/reset timer for seq
  - `Cancel(seq uint32)` — cancel timer (on ACK received)
  - `Stop()` — cancel all timers
  - Initial timeout: 500ms; double on each retransmit for that packet; cap at 60s
- [ ] `[B]` Unit tests: `timer_test.go` — start/cancel, expiry callback, backoff

### 3.5 RDT Sender (`internal/rdt/sender.go`)
- [ ] `[B]` Implement `RDTSender` struct:
  - `conn *net.UDPConn`, `sessionID uint32`, `remoteAddr *net.UDPAddr`
  - `state` enum: CLOSED, SYN_SENT, ESTABLISHED, DATA_TRANSFER, FIN_SENT
  - `sendBase uint32` — oldest unacknowledged sequence number (window left edge)
  - `nextSeqNum uint32` — next sequence number to assign
  - `windowSize uint32` — effective window: `min(cwnd, rwnd)`
  - `inflight map[uint32]*PacketRecord` — {seq: {data, sentTime, retransCount}}
  - `recvWindow uint16` — latest advertised rwnd from receiver
  - `bbr *BBR` — congestion controller reference
  - `outbox chan []byte` — application data to send
- [ ] `[B]` Methods:
  - `Connect(addr *net.UDPAddr) error` — 3-way handshake: send SYN → rcv SYN+ACK → send ACK
  - `Send(data []byte) error` — queue data in outbox; sender loop chunks it into packets
  - `sendLoop()` — goroutine:
    1. Read from outbox
    2. Split into 1400-byte chunks
    3. For each chunk: check inflight < min(cwnd, rwnd); block if full
    4. Assign seq, build header, compute checksum, send via UDP
    5. Record in inflight map, start timer
    6. Pace sends per BBR pacing_rate
  - `recvLoop()` — goroutine:
    1. Read UDP datagrams (ACKs)
    2. Verify checksum
    3. Parse ACK: mark packet delivered in inflight, cancel timer
    4. Advance sendBase to lowest unacked seq
    5. Report delivery rate and RTT to BBR
    6. Handle FIN+ACK → transition state
  - `Close() error` — send FIN, wait for FIN+ACK
- [ ] `[B]` Handle retransmission on timer expiry:
  - Resend the packet from inflight
  - Double the timer for that packet (exponential backoff)
  - After 3 consecutive timeouts for same packet → RST + error
- [ ] `[B]` Unit tests: `sender_test.go` — connect, send/receive ACK flow, retransmit on timeout, window enforcement

### 3.6 BBR Congestion Control (`internal/rdt/bbr.go`)
- [ ] `[B]` Implement `BBR` struct:
  - `state` enum: STARTUP, DRAIN, PROBE_BW, PROBE_RTT
  - `btlbw float64` — bottleneck bandwidth estimate (bytes/sec)
  - `rtprop time.Duration` — round-trip propagation time estimate
  - `cwnd uint32` — congestion window in bytes
  - `pacingRate float64` — bytes/sec
  - `pacingGain float64` — current gain multiplier
  - `latestRTT time.Duration` — most recent RTT sample
  - `minRTT time.Duration` — minimum RTT in current filter window
  - `deliveryRate float64` — current delivery rate estimate
  - `delivered uint64` — cumulative bytes delivered (ACKed)
  - `phase int` — PROBE_BW phase index (0-7)
  - `fullBwCount int` — STARTUP plateau counter
  - `lastProbeRTT time.Time` — last PROBE_RTT entry time
  - `probeRTTStart time.Time`
  - `bwFilter []float64` — ring buffer for Btlbw max filter
  - `rttFilter []time.Duration` — ring buffer for RTprop min filter
- [ ] `[B]` Implement `BBR` methods:
  - `NewBBR() *BBR` — initialize with defaults
  - `OnACK(delivered uint64, rtt time.Duration)` — core update:
    1. Compute delivery rate: `(newly_delivered_bytes) / (interval since last ACK)`
    2. Update `bwFilter` windowed max → `btlbw`
    3. Update `rttFilter` windowed min → `rtprop`
    4. State-specific logic (see below)
  - `OnSend(bytes uint32)` — track inflight
  - `CanSend() bool` — `inflight < cwnd`
  - `PacingDelay(bytes uint32) time.Duration` — `bytes / pacingRate`
  - `UpdateCwndAndRate()`:
    - `cwnd = max(pacingGain * btlbw * rtprop, 4 * MSS)`
    - `pacingRate = pacingGain * btlbw`
- [ ] `[B]` Implement state transitions:
  - **STARTUP**: `pacingGain = BBRStartupGain`. On each ACK: if `deliveryRate` hasn't increased by >25% for `fullBwCount` RTTs → `fullBwCount++`. When `fullBwCount >= BBRStartupFullBwCnt` → DRAIN.
  - **DRAIN**: `pacingGain = BBRDrainGain`. After inflight drops to ≤ BDP (or 1 RTT elapsed) → PROBE_BW.
  - **PROBE_BW**: Cycle phases 0-7. `pacingGain` = 1.25, 0.75, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0. Advance phase after ~1 RTT.
  - **PROBE_RTT**: Entry condition: `time.Since(lastProbeRTT) > BBRProbeRTTInterval`. Set cwnd = 4 * MSS for `BBRProbeRTTDuration` (200ms). After 200ms: reset min RTT filter, restore cwnd, return to PROBE_BW.
- [ ] `[B]` Unit tests: `bbr_test.go` — state transitions, gain changes, cwnd/pacing calculation, PROBE_RTT trigger

### 3.7 Server-Side RDT Integration
- [ ] `[A]` Integrate `RDTSender` into server's RETR handler:
  - After 150 reply, create RDT sender (or receiver for STOR)
  - Establish RDT connection per PORT/PASV mode
  - Stream file data via RDT sender
  - On completion: close RDT, send 226 reply
- [ ] `[A]` Integrate `RDTReceiver` into server's STOR handler:
  - Receive file data via RDT receiver
  - Write to file
  - On FIN: close file, send 226 reply

### 3.8 Client-Side RDT Integration
- [ ] `[B]` Integrate `RDTSender` into client's STOR handler
- [ ] `[B]` Integrate `RDTReceiver` into client's RETR handler
- [ ] `[B]` Implement ANSI terminal progress bar:
  - Show `[=====>    ] NN% | X.X MB/s | ETA: MM:SS`
  - Update on each chunk delivered/received
  - Use ANSI escape codes: `\r` + `\033[K`

---

## Phase 4: Integration, End-to-End Testing & Integrity Verification

### 4.1 Integration Tests (`test/integration/e2e_test.go`)
- [ ] `[Both]` Start test server in a goroutine, connect test client
- [ ] `[Both]` Test: Authentication (valid and invalid)
- [ ] `[Both]` Test: LIST, CWD, CDUP, MKD, RMD, DELE
- [ ] `[Both]` Test: RETR single file; verify byte-for-byte identity
- [ ] `[Both]` Test: STOR single file; verify byte-for-byte identity
- [ ] `[Both]` Test: Binary file transfer (image or archive)
- [ ] `[Both]` Test: HASH command — compute SHA-256; compare with `crypto/sha256` direct computation
- [ ] `[Both]` Test: PORT mode data transfer
- [ ] `[Both]` Test: PASV mode data transfer
- [ ] `[Both]` Test: Concurrent sessions (2+ clients simultaneously transferring)
- [ ] `[Both]` Test: Large file (>10MB) transfer with simulated packet loss (if feasible)
- [ ] `[Both]` Test: STOU unique filename generation
- [ ] `[Both]` Test: APPE append to existing file
- [ ] `[Both]` Test: RNFR/RNTO rename sequence (valid and invalid)

### 4.2 Test Fixtures (`test/fixtures/`)
- [ ] `[Both]` Create sample ASCII text file
- [ ] `[Both]` Create sample binary file (e.g., small PNG or ZIP)
- [ ] `[Both]` Create nested directory structure for LIST/CWD tests

### 4.3 Edge Cases & Robustness
- [ ] `[Both]` Handle 0-byte file transfer
- [ ] `[Both]` Handle file not found (550)
- [ ] `[Both]` Handle permission denied
- [ ] `[Both]` Handle invalid PORT/PASV addresses
- [ ] `[Both]` Handle session timeout / idle disconnect
- [ ] `[Both]` Handle ABOR mid-transfer: send RST on UDP, 426 on TCP
- [ ] `[Both]` Handle client disconnect during transfer (cleanup resources)
- [ ] `[Both]` Handle server shutdown while sessions active

---

## Phase 5: Technical Report & GenAI Audit Log

### 5.1 Technical Report (`docs/` or separate LaTeX/Word doc)
- [ ] `[Both]` Section 1: Application Scenario & Protocol Interaction — TCP+UDP lifecycle sequence diagram
- [ ] `[Both]` Section 2: Project-Wide Data Structures — TCP control packet format, UDP custom header, session structures
- [ ] `[Both]` Section 3: Functional Workflows — Server dispatch logic flowchart, RDT state machines, Active/Passive toggle flowcharts
- [ ] `[Both]` Section 4: Task Assignment Matrix — module owner per component (from AGENTS.md)
- [ ] `[Both]` Section 5: Self-Assessment & Peer Evaluation — individual evaluations + contribution percentages (must total 100%)
- [ ] `[Both]` Section 6: GenAI Usage & Code Refinement Log — complete appendix from `docs/GENAI_LOG.md`
- [ ] `[Both]` Section 7: Application Demo Evidence — screenshots/logs: upload, download, hash comparison, concurrent sessions

### 5.2 GenAI Audit Log (`docs/GENAI_LOG.md`)
- [ ] `[Both]` Log every AI-assisted coding session: prompt, raw output, refinements, final state
- [ ] `[Both]` Ensure the log demonstrates critical evaluation and refinement of AI-generated code

### 5.3 Demo Preparation
- [ ] `[Both]` Compile on clean machine (verify no missing dependencies)
- [ ] `[Both]` Prepare demo script: login → LIST → CWD → RETR (download) → STOR (upload) → HASH → concurrent sessions → QUIT
- [ ] `[Both]` Capture screenshots and terminal logs

---

## Task Summary by Developer

| Phase | Developer A (Server) | Developer B (Client) |
|-------|---------------------|---------------------|
| Phase 1 | Auth, Commands, Replies, Protocol, Session Manager, Server main, Control dispatch | Client main, CLI loop, Command formatting |
| Phase 2 | FS ops (PWD, CWD, MKD, LIST, SIZE, DELE, RNFR/RNTO, STOU, HASH server-side), PORT/PASV handler | Client-side PORT/PASV, command builders, local commands, HASH client-side |
| Phase 3 | Server-side RDT integration (RETR/STOR handlers) | RDT Header, Checksum, Sender, Receiver, Timers, BBR, Client-side RDT integration, Progress bar |
| Phase 4 | Integration tests (shared), edge cases | Integration tests (shared), edge cases |
| Phase 5 | Technical Report sections, GenAI log entries | Technical Report sections, GenAI log entries |
