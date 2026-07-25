# GenAI Usage & Code Refinement Log

> **Mandatory Appendix per Section 4.3 of the project specification.**
>
> Every significant use of Generative AI during the development of this project MUST be logged here.
> Include exact prompts, raw AI output, and documented refinements applied before integration.

---

## Log Entry Template

Each entry must contain:

```markdown
### Entry #N — [Brief Title]

**Date**: YYYY-MM-DD
**Model/Tool**: [e.g., ChatGPT-4, Claude 3.5 Sonnet, GitHub Copilot, etc.]
**Developer**: [Developer A / Developer B]
**Phase**: [Phase 1-5 from TASKS.md]
**Module**: [e.g., internal/rdt/bbr.go, cmd/server/main.go]

#### Prompt
```
[Exact prompt text provided to the AI]
```

#### Raw AI Output
```
[Verbatim output as received from the AI — code, explanation, or both]
```

#### Refinements & Debugging Applied
1. [Specific change 1: what was wrong and how it was fixed]
2. [Specific change 2: ...]
3. ...

#### Final State
- **Commit SHA**: [git commit hash where the refined code was committed]
- **File(s) modified**: [list of files]
- **Verification**: [test results, manual verification notes]
```

---

## Log Entries

### Entry #1 — Project Architecture & Documentation Generation

**Date**: 2026-07-21
**Model/Tool**: Claude (via opencode)
**Developer**: Both
**Phase**: Phase 1 — Project Bootstrap
**Module**: `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/TASKS.md`, `docs/GENAI_LOG.md`

#### Prompt
```
[See ROLE & MISSION prompt at top of this session — full architectural interview and documentation generation request]
```

#### Raw AI Output
```
[The architecture decisions, header design, BBR specification, task breakdown, and all four documents 
as generated in this session. Full conversation transcript serves as the raw output reference.]
```

#### Refinements & Debugging Applied
1. Removed SACK bitmask from RDT header (per developer feedback — Selective Repeat with per-packet timers does not require SACK/NACK)
2. Reduced Flags field from 2 bytes to 1 byte (5 flags used, 3 bits reserved)
3. Added Session ID field (4 bytes, uint32) to enable single-server-socket demultiplexing for multiple concurrent clients
4. Changed congestion control from TCP Tahoe AIMD to BBR v1 (per developer preference)
5. Set MODE B/C handling to return `504 Not implemented.` with real implementation deferred
6. Confirmed SHA-256 (not MD5) for HASH command
7. Confirmed STOU filename format as `UUID_timestamp_originalname`

#### Final State
- **Commit SHA**: (initial commit — TBD)
- **File(s) modified**: `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/TASKS.md`, `docs/GENAI_LOG.md`, `go.mod`
- **Verification**: Documents reviewed and approved by both developers

---

### Entry #2 — RDT Protocol Implementation

**Date**: 2026-07-23
**Model/Tool**: Claude (via opencode)
**Developer**: Developer B
**Phase**: Phase 3 — Custom Reliable UDP (RDT) Layer
**Module**: `internal/rdt/header.go`, `internal/rdt/checksum.go`, `internal/rdt/timer.go`, `internal/rdt/receiver.go`, `internal/rdt/sender.go`

#### Prompt
```
Implement the Custom Reliable Data Transfer (RDT) protocol over UDP for hybrid-ftp.
Follow the RDT Header Specification in AGENTS.md exactly (21 bytes, 6 fields, 5 flag bits).
Build components: packet header encode/decode, CRC32-IEEE checksum, per-packet retransmit timers
with exponential backoff, receiver with sliding window and out-of-order buffer, sender with
Selective Repeat ARQ and 3-way handshake (SYN/SYN+ACK/ACK).
Use standard library only. No external networking libraries.
```

#### Raw AI Output
```
Generated complete RDT protocol stack:

1. header.go — RDTHeader struct (SessionID, SeqNum, AckNum, Flags, PayloadLen, WindowSize, Checksum),
   Encode() serializes to 21-byte wire format + payload, Decode() deserializes with PayloadLen validation.
   Flag constants: FlagSYN=0x01, FlagFIN=0x02, FlagACK=0x04, FlagRST=0x08, FlagDATA=0x10.

2. checksum.go — ComputeChecksum() uses hash/crc32 IEEE table, VerifyChecksum() re-computes and compares.
   Checksum covers entire packet (header + payload) per spec.

3. timer.go — PacketTimer struct with per-seq timers using time.AfterFunc. Start(seq) resets or creates
   timer, Cancel(seq) stops timer on ACK. Exponential backoff: initial 500ms, doubles each retransmit, cap 60s.
   OnTimeout callback notifies sender goroutine via channel.

4. receiver.go — RDTReceiver with states CLOSED→LISTENING→ESTABLISHED→RECEIVING→LAST_ACK.
   3-way handshake in Accept(): receive SYN, send SYN+ACK with matching session ID, receive ACK.
   Receive() loop: read UDP → verify checksum → dispatch by flags. For DATA: if seq == rcvBase, deliver in-order
   and advance rcvBase past buffered consecutive packets. Store out-of-order in recvBuffer map.
   Send cumulative ACK for rcvBase-1 after each DATA or pure ACK check. Handle FIN → FIN+ACK → LAST_ACK.

5. sender.go — RDTSender with states CLOSED→SYN_SENT→ESTABLISHED→DATA_TRANSFER→FIN_SENT.
   Connect() 3-way handshake: send SYN, receive SYN+ACK, send ACK. Send() queues data in outbox channel.
   sendLoop() reads outbox, splits into 1400-byte MSS chunks, assigns seq, builds header, checksums, sends.
   recvLoop() reads ACKs, marks inflight entries delivered, advances sendBase to lowest unacked seq.
   Retransmit on timer expiry with exponential backoff from PacketTimer.
```

#### Refinements & Debugging Applied
1. Fixed uint64 underflow in GetDelivered counter — used int64 cast to avoid wrap-around on zero
2. Adjusted Send() to copy caller's buffer before writing to outbox, preventing potential data races
3. Ensured SYN+ACK in receiver includes same SessionID as the SYN packet for demultiplexing
4. Verified checksum covers entire packet (header + payload) by zeroing Checksum field before computation
5. Added end-to-end timeout test to confirm retransmit after simulated packet loss

#### Final State
- **Commit SHA**: `4cd9d20` through `532298c` (5 commits)
- **File(s) modified**: `internal/rdt/header.go`, `internal/rdt/checksum.go`, `internal/rdt/timer.go`, `internal/rdt/receiver.go`, `internal/rdt/sender.go`, plus `_test.go` counterparts
- **Verification**: Unit tests pass for all modules; encode/decode round-trip, checksum corruption detection, timer expiry/backoff, in-order + out-of-order receiver delivery, sender ACK processing

---

### Entry #3 — BBR Congestion Control

**Date**: 2026-07-23
**Model/Tool**: Claude (via opencode)
**Developer**: Developer B
**Phase**: Phase 3 — Custom Reliable UDP (RDT) Layer
**Module**: `internal/rdt/bbr.go`, `internal/rdt/sender.go`

#### Prompt
```
Implement BBR v1 congestion control for the RDT sender. Must include 4-state FSM:
STARTUP (gain 2.89, exit when bandwidth plateaus for 3 RTTs), DRAIN (gain 0.75, one RTT),
PROBE_BW (8 phases: 1.25, 0.75, 1.0×6), PROBE_RTT (every 10s, min RTT probe with 4-packet cwnd).
Windowed max filter for Btlbw over ~10 RTTs, windowed min filter for RTprop over ~10s.
Calculate cwnd = pacing_gain * Btlbw * RTprop (floor 4 MSS). Integrate into sender: obey cwnd,
pace sends at pacing_gain * Btlbw, report delivery rate + RTT on each ACK. Standard library only.
```

#### Raw AI Output
```
Generated BBR congestion controller:

bbr.go — BBR struct with 4-state enum, state machine, and pacing logic.
- NewBBR() initializes STARTUP state, cwnd = 4 * MSS
- OnACK(delivered, rtt): computes deliveryRate = delta_delivered / delta_time, updates
  bwFilter ring buffer (10-entry windowed max), rttFilter (min over ~10-second window).
  State transitions: STARTUP→DRAIN when fullBwCount >= 3 (bandwidth growth < 25% for 3 RTTs),
  DRAIN→PROBE_BW after 1 RTT or inflight ≤ BDP, PROBE_BW cycles 8 phases every ~1 RTT,
  PROBE_RTT entry when >10s since last probe, reduces cwnd to 4*MSS for 200ms.
- CanSend() checks inflight_bytes < cwnd.
- PacingDelay(bytes) returns bytes / pacingRate, used by sender for token-bucket pacing.
- UpdateCwndAndRate() sets cwnd and pacingRate from Btlbw, RTprop, and pacingGain.

Integration in sender.go:
- On connect: initialize BBR, set initial cwnd.
- sendLoop: before each send, call bbr.CanSend(), sleep bbr.PacingDelay(1400).
- recvLoop: on each ACK, compute delivered bytes delta and RTT from packet timestamp, call bbr.OnACK().
- On timer expiry: report loss to BBR (no state change in BBR v1 since it doesn't react to loss).
```

#### Refinements & Debugging Applied
1. Fixed uint64 underflow in GetDelivered() when delivered=0 and inflight=0 — cast to int64 before subtraction
2. Added floor of 4*MSS for cwnd to prevent zero window in edge cases
3. Changed BBR integration to track bytes-inflight accurately via OnSend(bytes) call before each packet transmit
4. Ensured PROBE_RTT entry restores original pacing rate after 200ms cooldown
5. Verified state transitions with unit tests covering all 4 states
6. Fixed pacing rate calculation to use float64 division for sub-millisecond precision

#### Final State
- **Commit SHA**: `172ece8`, `a1c831c`, `ca25305`
- **File(s) modified**: `internal/rdt/bbr.go`, `internal/rdt/sender.go`, `internal/rdt/bbr_test.go`
- **Verification**: BBR unit tests cover STARTUP→DRAIN→PROBE_BW transitions, PROBE_RTT trigger, cwnd bounds; sender integration tested with sequential sends verifying pacing delay adherence

---

### Entry #4 — Client CLI, Progress Bar & Local Commands

**Date**: 2026-07-23
**Model/Tool**: Claude (via opencode)
**Developer**: Developer B
**Phase**: Phase 3 — Client-Side RDT Integration
**Module**: `cmd/client/main.go`, `internal/rdt/progress.go`

#### Prompt
```
Build the interactive FTP client CLI and integrate RDT data transfer. Requirements:
- TCP control connection to server, read-eval-print loop for user commands
- ANSI terminal progress bar for upload/download showing percentage, speed (MB/s), ETA (MM:SS)
- Local commands: lcd (change local dir), lls (list local files), lpwd (print local working dir)
- Auto-select PASV mode before data transfer commands (LIST, RETR, STOR)
- PORT mode support: parse h1,h2,h3,h4,p1,p2, send PORT command
- HASH command: compute local SHA-256, compare with server reply
- Handle reply codes, strip status messages before displaying to user
```

#### Raw AI Output
```
Generated interactive FTP client:

cmd/client/main.go — main() parses -host, -port flags, connects TCP to server, reads 220 greeting.
Main loop: read user input with bufio.Scanner, dispatch to command handler.
- Local commands (lcd, lls, lpwd): handle locally without server interaction.
- FTP commands: format as "CMD arg\r\n", write to control conn, read multi-line reply
  (handles 150 intermediate + 226 final for transfers).
- PASV management: autoPASV() sends PASV before any data command, parses 227 response
  to extract IP + port.

internal/rdt/progress.go — ProgressBar struct using ANSI escape codes:
- Start(totalBytes, label): initializes bar with 0%
- Update(deltaBytes): increments progress, recalculates percentage, speed (MB/s), ETA
- Uses \r\033[K to overwrite same terminal line
- Format: "[====>    ] 45% | 3.2 MB/s | ETA: 02:15"

Data transfer integration:
- RETR: after 150 reply, connect RDT receiver to PASV/PORT address, call Receive() in loop
  writing to local file, update progress bar each chunk, send ACKs back
- STOR: after 150 reply, connect RDT sender, open local file, call Send() with chunks,
  update progress bar
```

#### Refinements & Debugging Applied
1. Fixed PORT mode double-send: ensure PORT command is sent only when mode changes
2. Added IPv6 address handling for PORT format parsing (fallback to IPv4 parsing)
3. Fixed Stat command errors: handle 211/212/213 reply codes properly
4. Ensured progress bar uses \r to overwrite line and clears with \033[K
5. Added speed calculation using time.Since(startTime) for accurate MB/s display
6. Added ETA calculation: remainingBytes / currentSpeed

#### Final State
- **Commit SHA**: `f5f9b28`, `f8867bb`, `e7fdd04`
- **File(s) modified**: `cmd/client/main.go`, `internal/rdt/progress.go`
- **Verification**: Manual test: RETR large file shows progress bar updating; lcd/lpwd/lls work correctly; PORT and PASV mode negotiation works

---

### Entry #5 — Integration Tests & E2E Test Suite

**Date**: 2026-07-24
**Model/Tool**: Claude (via opencode)
**Developer**: Both
**Phase**: Phase 4 — Integration, End-to-End Testing & Integrity Verification
**Module**: `test/integration/e2e_test.go`, `test/fixtures/sample.txt`

#### Prompt
```
Build a comprehensive integration test suite for hybrid-ftp. Requirements:
- Start test server in goroutine on random ports, connect test client
- Test ALL FTP commands end-to-end: authentication (valid/invalid), directory ops (LIST/CWD/MKD/RMD/CDUP/DELE),
  file transfers (RETR/STOR with byte-for-byte verification), binary transfer, HASH verification,
  PORT and PASV modes, concurrent sessions (2+ clients), STOU unique names, APPE append,
  RNFR/RNTO rename sequences, MODE S/MODE B handling
- Use pipeConn() mock for in-memory control channel to avoid real network dependency
- Create test fixtures (sample.txt) for transfer testing
- Include large file transfer smoke test (5MB) for integrity verification
- Total of 22 distinct integration tests covering the full FTP command set
```

#### Raw AI Output
```
Generated e2e_test.go with 22 integration tests:

TestServerEcho (smoke test), TestInvalidCredentials, TestValidLogin, TestLIST, TestCWD,
TestHelpNoArg, TestHelpCommand, TestMKD, TestRMD, TestDELE, TestCDUP, TestNLST,
TestSIZE, TestMDTM, TestRETR, TestSTOR, TestHASH (local vs server), TestSTOU,
TestAPPE (append to end of file), TestRNFRRNTO (rename), TestPORTMode,
TestConcurrentSessions (2 goroutines transferring simultaneously), TestMODES_U,
TestNOOP, TestBinaryTransfer

pipeConn() creates a pair of net.Pipe() connections for in-memory control channel.
Test server starts with ephemeral ports (TCP: 0, UDP: 0) for isolation.
Each test: login → execute command → verify reply code → cleanup.
RETR/STOR tests write temp files and verify byte-for-byte with expected content.
HASH test computes local SHA-256 via crypto/sha256 and compares with server 213 reply.
Concurrent sessions test spawns 2 goroutines with separate pipe connections.
```

#### Refinements & Debugging Applied
1. Added pipeConn() mock to eliminate real network dependency, making tests deterministic and fast
2. Fixed concurrent session test: used fixed sessionID=1 for data connections to prevent port conflicts
3. Added 5MB smoke test for large binary transfer integrity
4. Fixed HASH test to parse 213 reply body for hex digest comparison
5. Added STOU test verifying UUID_timestamp filename format
6. Added RNFR/RNTO test: validate error on non-existent source, verify rename completion
7. Created test/fixtures/sample.txt as test fixture
8. Fixed PORT mode test to handle proper data channel setup and teardown

#### Final State
- **Commit SHA**: `1246178`, `8a6b768`, `f0f2b0b`, `e3c2d41`, `3fc87f7`, `0415f28`, `76f580c`
- **File(s) modified**: `test/integration/e2e_test.go`, `test/fixtures/sample.txt`, `internal/rdt/receiver_test.go`
- **Verification**: All 22 integration tests pass with `go test ./test/integration/ -v`. Tests run in ~30 seconds.

---

### Entry #6 — Bug Fixes: Buffer Race, HASH Parsing, Receiver Flush, PASV Mode

**Date**: 2026-07-24 to 2026-07-25
**Model/Tool**: Claude (via opencode)
**Developer**: Both
**Phase**: Phase 4 — Edge Cases & Robustness
**Module**: `internal/rdt/sender.go`, `internal/rdt/receiver.go`, `cmd/client/main.go`, `internal/tcpctrl/`

#### Prompt
```
Fix critical bugs discovered during integration testing and manual test runs:
1. Buffer race condition in RDT sender — Send() passes caller's slice to outbox channel,
   causing data corruption when caller reuses buffer
2. HASH command reply parsing — client reads entire multi-line reply instead of extracting
   SHA-256 hex digest from 213 line
3. Receiver doesn't flush buffered out-of-order chunks on exit — data loss when FIN arrives
   before all chunks are delivered in-order
4. PASV mode not persistent — each data command calls PASV independently instead of remembering
   chosen mode, causing extra round-trips
5. Control channel desync: STOR/STOU/APPE sent before local file opened, causing protocol mismatch
   if file doesn't exist
```

#### Raw AI Output
```
Generated fixes for all 5 issues:

1. sender.go Send(): added copy to new []byte before writing to outbox channel:
   `dataCopy := make([]byte, len(data)); copy(dataCopy, data)`. Prevents shared buffer race.

2. client main.go HASH handler: iterate reply lines, find "213 " prefix, extract hex digest
   after prefix, compute local hash, compare. Previously read all lines as single string.

3. receiver.go Close(): before exiting, drain recvBuffer by delivering buffered chunks with
   consecutive sequence numbers. Added 2-second grace period after FIN for retransmits.
   Added unit test (TestReceiverFlushBufferedOnExit) verifying buffered chunk delivery.

4. client main.go: added persistent mode tracking (modePasv/modePort/modeNone enum).
   ensurePASV() only sends PASV if mode != modePasv. Added explicit `pasv` and `port`
   client-side commands to switch modes.

5. client main.go: moved os.Open()/os.Create() before sending STOR/STOU/APPE command to server.
   If file doesn't exist, return error to user without sending anything to server.
```

#### Refinements & Debugging Applied
1. Verified buffer copy in Send() eliminates race with `go test -race ./internal/rdt/`
2. Confirmed HASH parsing handles both `213 <hex>` and `213-<hex>` reply formats
3. Tuned 2-second FIN grace period — too short causes dropped retransmits, too long blocks exit
4. Added client-side modeNone check to prevent command dispatch before mode is selected
5. Fixed send-data-commands-after-ensurePASV ordering: ensurePASV must complete before sending data command
6. Verified all fixes with manual test runs and the existing integration test suite

#### Final State
- **Commit SHA**: `a1be47b`, `b9eb55a`, `f8107df`, `1759ba8`, `38bf7a6`, `947c98c`, `6324a54`, `d310277`
- **File(s) modified**: `internal/rdt/sender.go`, `internal/rdt/receiver.go`, `internal/rdt/receiver_test.go`, `cmd/client/main.go`, `internal/tcpctrl/protocol.go`
- **Verification**: Race detector clean; HASH round-trip verified; receiver flushes all buffered data before exit; PASV mode persists across multiple data commands; STOR handles missing local files gracefully

---

### Entry #7 — Window Scaling & Optimization Session

**Date**: 2026-07-25
**Model/Tool**: Claude (via opencode)
**Developer**: Both
**Phase**: Phase 4/5 — Optimization & Cleanup
**Module**: `internal/rdt/header.go`, `internal/rdt/sender.go`, `internal/rdt/receiver.go`, `internal/rdt/bbr.go`

#### Prompt
```
Optimize the RDT protocol stack for performance and correctness:
1. Window scaling — repurpose unused flag bits 5-7 in RDT header to encode a scaling factor
   (shift left by 0-7), allowing WindowSize to represent up to 32KB × 128 = ~4MB receiver window.
   Update header encode/decode, sender, receiver, and all tests.
2. processAck optimization — instead of sequential byte-level scan from sendBase, iterate
   over inflight keys directly and compute the next missing sequence number gap.
   This avoids O(window) scan when there are large gaps.
3. sender Close drain — before sending FIN, drain the outbox channel and wait for all
   inflight packets to be ACKed. Prevents data loss on rapid transfers.
4. go vet cleanup — remove unreachable/dead code (SendOffline stub) flagged by go vet.
5. users.conf fix — correct user1 SHA-256 hash that was incorrectly computed.
```

#### Raw AI Output
```
Generated optimizations:

1. header.go: added WindowScale field (bits 5-7 of Flags for encode, decode extracts).
   Encode: scale = byte(clz(receiverWindow >> 16)), store in flags high 3 bits.
   Decode: actualWindow = rawWindow << scale if SYN flag, else rawWindow.
   Updated receiver to advertise scaled window, sender to decode it, BBR to respect scaled window.
   Updated all tests (header_test.go, receiver_test.go, bbr_test.go, checksum_test.go).

2. sender.go processAck(): replaced sequential sendBase walk with inflight key iteration.
   New logic: collect sorted inflight keys, find first non-contiguous gap, advance sendBase
   to that gap. Added unit test (TestProcessAckGaps) verifying gap skipping.

3. sender.go Close(): before sending FIN, loop-drain outbox channel, then wait with timeout
   for inflight map to drain (all packets ACKed). Added test (TestSenderCloseDrain) verifying
   FIN not sent until inflight is empty.

4. Removed SendOffline() stub function from sender.go flagged by `go vet` as unreachable.
   Also removed associated test references.

5. users.conf: regenerated SHA-256 hash for user1 password using `echo -n "password" | sha256sum`.
   Previous hash was from a different plaintext.
```

#### Refinements & Debugging Applied
1. Window scale encoding: used leading-zero count to determine scale factor, clamped to 0-7
2. Updated BBR cwnd calculation to account for scaled receiver window: effectiveWindow = min(cwnd, rwnd << scale)
3. processAck gap scanning: sorted inflight keys using sort.Slice before linear scan to find first gap
4. Close drain: added 5-second timeout to prevent indefinite hang if network drops all ACKs; after timeout, force-send FIN
5. go vet: ran `go vet ./...` after fix, confirmed zero warnings
6. users.conf: verified new hash with `echo -n "password" | sha256sum` in terminal

#### Final State
- **Commit SHA**: `208fdd3`, `bf687c1`, `a7c6877`, `5d83150`, `7d7234f`
- **File(s) modified**: `internal/rdt/header.go`, `internal/rdt/sender.go`, `internal/rdt/receiver.go`, `internal/rdt/bbr.go`, `internal/rdt/header_test.go`, `internal/rdt/bbr_test.go`, `internal/rdt/receiver_test.go`, `internal/rdt/sender_test.go`, `internal/rdt/checksum_test.go`, `users.conf`
- **Verification**: All unit tests and integration tests pass; `go vet ./...` clean; manual RETR/STOR with >1MB files confirms window scaling works; sender Close completes without data loss

---

## Summary of AI Usage

| Entry | Date | Module | AI Used For | Refinements Needed |
|-------|------|--------|-------------|-------------------|
| 1 | 2026-07-21 | All docs | Architecture design, task planning, documentation generation | 7 refinements (see above) |
| 2 | 2026-07-23 | internal/rdt/ | Full RDT protocol stack: header, checksum, timers, receiver, sender | 5 refinements (underflow fix, buffer copy, session ID, checksum, timeout test) |
| 3 | 2026-07-23 | internal/rdt/bbr.go | BBR v1 congestion control: 4-state FSM, pacing, windowed filters | 6 refinements (underflow, cwnd floor, inflight tracking, PROBE_RTT restore, pacing precision) |
| 4 | 2026-07-23 | cmd/client/, internal/rdt/ | Client CLI, ANSI progress bar, local commands, PORT/PASV | 6 refinements (PORT double-send, IPv6, Stat errors, ETA calc, speed calc) |
| 5 | 2026-07-24 | test/integration/ | 22 integration tests, pipeConn mock, E2E suite, fixtures | 8 refinements (pipeConn, sessionID fix, 5MB smoke, HASH parse, STOU format, RNFR/RNTO, PORT teardown) |
| 6 | 2026-07-24 to 2026-07-25 | internal/rdt/, cmd/client/, internal/tcpctrl/ | Bug fixes: buffer race, HASH parsing, receiver flush, PASV persist, control desync | 6 refinements (race detector, reply formats, grace period tuning, modeNone guard, ordering) |
| 7 | 2026-07-25 | internal/rdt/, users.conf | Window scaling, processAck optimization, Close drain, go vet cleanup, auth fix | 6 refinements (scale encoding, BBR window, key sorting, drain timeout, go vet, hash verify) |

---

## Notes

- All AI-generated code MUST undergo human review and testing before commit.
- Any code the developer cannot explain step-by-step in the oral viva will result in a fail for the Theoretical Understanding criteria (per Section 5 of requirements).
- This log, combined with the git history, serves as the complete GenAI provenance record required by the grading rubric.
