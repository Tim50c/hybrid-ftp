# Architecture Specification — Hybrid FTP

## 1. System Overview

Hybrid FTP decouples the FTP control plane (TCP) from the data plane (Custom Reliable UDP). The control channel handles authentication, command negotiation, and status reporting. The data channel handles file payload transfer with application-layer reliability built directly on UDP — providing zero packet loss, corruption detection, duplicate elimination, and correct ordering.

```
┌──────────────────────────────────────────────────────────────────┐
│                         CONTROL CHANNEL (TCP)                     │
│                                                                    │
│  Client ──USER/PASS──────────────► Server                        │
│  Client ──PORT/PASV──────────────► Server                        │
│  Client ──RETR/STOR──────────────► Server                        │
│  Client ◄──220/230/226/550─────── Server                         │
│                                                                    │
├──────────────────────────────────────────────────────────────────┤
│                         DATA CHANNEL (Custom RDT over UDP)        │
│                                                                    │
│  ┌──────────┐                          ┌──────────┐              │
│  │  RDT     │──SYN──────────────────►  │  RDT     │              │
│  │  Sender  │◄──SYN+ACK─────────────── │  Receiver│              │
│  │          │──DATA(seq=1)───────────► │          │              │
│  │          │──DATA(seq=2)───────────► │          │              │
│  │          │◄──ACK(seq=1)──────────── │          │              │
│  │          │──DATA(seq=3)───────────► │          │              │
│  │          │◄──ACK(seq=2)──────────── │          │              │
│  │          │──FIN──────────────────►  │          │              │
│  │          │◄──FIN+ACK─────────────── │          │              │
│  └──────────┘                          └──────────┘              │
└──────────────────────────────────────────────────────────────────┘
```

---

## 2. TCP Control Protocol

### 2.1 Command Set

| Command | Arguments | Requires Auth | Handled By |
|---------|-----------|---------------|------------|
| USER | username | No | auth.go |
| PASS | password | No | auth.go |
| QUIT | — | No | session/manager.go |
| NOOP | — | No | tcpctrl/protocol.go |
| PWD | — | Yes | fs/ops.go |
| CWD | path | Yes | fs/ops.go |
| CDUP | — | Yes | fs/ops.go |
| MKD | dirname | Yes | fs/ops.go |
| RMD | dirname | Yes | fs/ops.go |
| LIST | [path] | Yes | fs/ops.go |
| NLST | [path] | Yes | fs/ops.go |
| STAT | [path] | Yes | fs/ops.go |
| SIZE | filename | Yes | fs/ops.go |
| MDTM | filename | Yes | fs/ops.go |
| TYPE | A or I | Yes | tcpctrl/protocol.go |
| MODE | S, B, or C | Yes | tcpctrl/protocol.go |
| PORT | h1,h2,h3,h4,p1,p2 | Yes | tcpctrl/protocol.go |
| PASV | — | Yes | tcpctrl/protocol.go |
| RETR | filename | Yes | fs/ops.go + rdt/sender.go |
| STOR | filename | Yes | fs/ops.go + rdt/receiver.go |
| STOU | — | Yes | fs/ops.go |
| APPE | filename | Yes | fs/ops.go |
| DELE | filename | Yes | fs/ops.go |
| RNFR | oldname | Yes | fs/ops.go |
| RNTO | newname | Yes | fs/ops.go |
| HASH | filename | Yes | fs/ops.go |
| ABOR | — | Yes | tcpctrl/protocol.go |
| HELP | [command] | No | tcpctrl/protocol.go |

### 2.2 Reply Codes

| Code | Meaning | Usage |
|------|---------|-------|
| 110 | Restart marker reply | Not used |
| 120 | Service ready in n minutes | Not used |
| 125 | Data connection already open | Transfer starting; data channel established |
| 150 | File status okay; about to open data connection | Preceding RETR/STOR/LIST |
| 200 | Command okay | Generic success |
| 202 | Command not implemented (superfluous) | Not used |
| 211 | System status or help reply | STAT, HELP response |
| 212 | Directory status | STAT on directory |
| 213 | File status | STAT on file |
| 214 | Help message | HELP response |
| 215 | NAME system type | Not used |
| 220 | Service ready for new user | Initial greeting |
| 221 | Service closing control connection | QUIT response |
| 225 | Data connection open; no transfer in progress | Post-transfer idle |
| 226 | Closing data connection; transfer complete | Successful transfer |
| 227 | Entering Passive Mode (h1,h2,h3,h4,p1,p2) | PASV response |
| 230 | User logged in; proceed | Successful authentication |
| 250 | Requested file action okay | MKD, RMD, DELE, CWD, RNTO success |
| 257 | Pathname created | MKD with full path |
| 331 | User name okay; need password | After USER |
| 332 | Need account for login | Not used |
| 350 | Requested file action pending further information | After RNFR |
| 421 | Service not available; closing control connection | Server shutdown |
| 425 | Can't open data connection | Data channel failure |
| 426 | Connection closed; transfer aborted | ABOR or timeout |
| 450 | Requested file action not taken | File busy / locked |
| 451 | Requested action aborted; local error | Server-side error |
| 452 | Requested action not taken; insufficient storage | Disk full |
| 500 | Syntax error; command unrecognized | Unknown command |
| 501 | Syntax error in parameters or arguments | Bad args |
| 502 | Command not implemented | For future extension |
| 503 | Bad sequence of commands | RNTO without RNFR, etc. |
| 504 | Command not implemented for that parameter | MODE B, MODE C |
| 530 | Not logged in | Command issued before USER/PASS |
| 532 | Need account for storing files | Not used |
| 550 | Requested action not taken; file unavailable | No such file, permission denied |
| 551 | Requested action aborted; page type unknown | Not used |
| 552 | Requested file action aborted; exceeded storage | Not used |
| 553 | Requested action not taken; filename not allowed | Invalid filename |

### 2.3 MODE Command Handling

- `MODE S` (Stream): Supported — data sent as continuous byte stream.
- `MODE B` (Block): `504 Not implemented.` — deferred.
- `MODE C` (Compressed): `504 Not implemented.` — deferred.

---

## 3. Custom RDT Protocol (Reliable Data Transfer over UDP)

### 3.1 Packet Header (21 bytes)

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                         Session ID (32)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      Sequence Number (32)                      |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                       ACK Number (32)                          |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Flags(8)|    Payload Length (16)    |   Window Size (16)       |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Checksum (32)                           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
|                   Payload (0 - Payload Length bytes)            |
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### 3.2 Field Descriptions

| Offset | Size | Field | Type | Description |
|--------|------|-------|------|-------------|
| 0 | 4 | SessionID | uint32, big-endian | Unique per client session; demultiplexes UDP datagrams |
| 4 | 4 | SeqNum | uint32, big-endian | For DATA/SYN/FIN: this packet's sequence number. For pure-ACK: 0. |
| 8 | 4 | AckNum | uint32, big-endian | The sequence number being acknowledged. Valid when ACK flag set. |
| 12 | 1 | Flags | uint8 | See flag table below |
| 13 | 2 | PayloadLen | uint16, big-endian | Payload bytes following header. 0 for control packets. Max: 1400. |
| 15 | 2 | Window | uint16, big-endian | Receiver-advertised buffer window in bytes (rwnd) |
| 17 | 4 | Checksum | uint32, big-endian | CRC32-IEEE over header (bytes 0-16) + payload. Checksum field set to 0 during computation. |
| 21 | var | Payload | []byte | Application data (file chunk). Only present if DATA flag set and PayloadLen > 0. |

### 3.3 Flag Encoding (uint8)

| Bit | Mask | Constant | Name | Meaning |
|-----|------|----------|------|---------|
| 0 | 0x01 | FlagSYN | SYN | Connection initiation handshake |
| 1 | 0x02 | FlagFIN | FIN | Graceful close handshake |
| 2 | 0x04 | FlagACK | ACK | Acknowledgment; AckNum field is valid |
| 3 | 0x08 | FlagRST | RST | Abrupt connection reset |
| 4 | 0x10 | FlagDATA | DATA | Payload follows header; PayloadLen > 0 |
| 5-7 | — | — | Reserved | Must be 0 |

Valid flag combinations:
- `SYN` — connection request
- `SYN | ACK` — connection acknowledgment
- `ACK` — pure acknowledgment (no data)
- `DATA` — data packet (no ACK piggybacked)
- `DATA | ACK` — data packet with piggybacked ACK
- `FIN` — close request
- `FIN | ACK` — close acknowledgment
- `RST` — reset (any state)

### 3.4 Payload Constraints

- Maximum payload per datagram: **1400 bytes**
- Ensures total UDP datagram ≤ 1453 bytes (21 header + 1400 payload + 8 UDP header + 20 IP header ≈ 1449 bytes), safely under the typical 1500-byte Ethernet MTU.
- Files are split into fixed-size chunks at the RDT sender and reassembled at the RDT receiver.

### 3.5 Sequence Number Space

- 32-bit unsigned; wraps at 2^32.
- Initial Sequence Number (ISN) randomly chosen per session.
- Sequence numbers are **byte-oriented**: SeqNum advances by PayloadLen per DATA packet.

---

## 4. RDT State Machines

### 4.1 Sender State Machine

```
                    ┌─────────┐
                    │  CLOSED │
                    └────┬────┘
                         │ app: connect() → send SYN
                         ▼
                    ┌──────────┐
              ┌────►│SYN_SENT  │
              │     └────┬─────┘
              │ rcv RST  │ rcv SYN+ACK → send ACK
              │          ▼
              │     ┌─────────────┐
              │     │ ESTABLISHED │◄───────────────┐
              │     └──────┬──────┘                 │
              │            │ app: send(data)         │
              │            ▼                        │
              │     ┌──────────────┐                │
              │     │ DATA_TRANSFER├───timeout──┐   │
              │     └──────┬───────┘   retrans   │   │
              │            │ app: close()         │   │
              │            │ → send FIN           │   │
              │            ▼                      │   │
              │     ┌───────────┐                 │   │
              │     │ FIN_SENT  │                 │   │
              │     └─────┬─────┘                 │   │
              │           │ rcv FIN+ACK            │   │
              │           │ → send ACK             │   │
              │           ▼                        │   │
              │     ┌──────────┐                  │   │
              │     │  CLOSED  │                  │   │
              │     └──────────┘                  │   │
              │                                    │   │
              └────────────────────────────────────┘   │
                    any state: rcv RST → CLOSED        │
                                                       │
              On any ACK received: cancel per-packet timer,
              slide window, notify BBR of delivery
```

**DATA_TRANSFER substate — Selective Repeat:**

- Maintain a sliding window `[base, base+N)` where `N = min(cwnd, rwnd) / MSS`.
- Each packet in window has its own retransmit timer.
- On ACK for seq `s`: mark packet `s` delivered; cancel its timer; advance `base` to the lowest unacknowledged seq.
- On timer expiry for seq `s`: retransmit packet `s`; double its timer (exponential backoff).
- BBR provides `cwnd`; receiver provides `rwnd` via the Window field.
- `inflight` = bytes sent but not yet ACKed. Sender blocks when `inflight >= min(cwnd, rwnd)`.

### 4.2 Receiver State Machine

```
                    ┌─────────┐
                    │  CLOSED │
                    └────┬────┘
                         │ rcv SYN → send SYN+ACK
                         ▼
                    ┌──────────┐
                    │LISTENING │
                    └────┬─────┘
                         │ rcv ACK (completing 3-way handshake)
                         ▼
                    ┌─────────────┐
                    │ ESTABLISHED │◄─────────────────┐
                    └──────┬──────┘                   │
                           │ rcv DATA                 │
                           ▼                          │
                    ┌──────────────┐                  │
                    │   RECEIVING  │                  │
                    └──────┬───────┘                  │
                           │ rcv FIN → send FIN+ACK   │
                           ▼                          │
                    ┌──────────┐                      │
                    │ LAST_ACK │                      │
                    └─────┬────┘                      │
                          │ rcv ACK → CLOSED          │
                          ▼                           │
                    ┌──────────┐                      │
                    │  CLOSED  │                      │
                    └──────────┘                      │
                                                      │
                    any state: rcv RST → CLOSED        │
```

**RECEIVING substate — Selective Repeat:**

- Maintain a **reorder buffer**: map and/or ring buffer of received packets indexed by sequence number.
- For each received DATA packet:
  1. Verify CRC32 checksum. If invalid, silently discard.
  2. If duplicate (already in buffer), discard data but resend ACK.
  3. If in-order (`seq == rcv_base`): deliver payload to application layer up to the next gap; advance `rcv_base`; send ACK for `rcv_base - 1`.
  4. If out-of-order but within window: buffer it; send ACK for the last in-order packet (`rcv_base - 1`).
  5. If out-of-order and outside window: send ACK for `rcv_base - 1` (stale ACK).
- Advertise `rwnd` = buffer_remaining in the Window field of every ACK.
- Send ACK for every received DATA packet (no delayed ACK).

---

## 5. BBR Congestion Control — Detailed Specification

### 5.1 Model Parameters

| Parameter | Symbol | Initial Value | Update |
|-----------|--------|---------------|--------|
| Bottleneck Bandwidth | `btlbw` | 0 | `max(windowed_delivery_rate)` over ~10 RTTs |
| Round-trip Propagation Time | `rtprop` | +∞ | `min(windowed_rtt)` over ~10 seconds |
| Congestion Window | `cwnd` | Derived | `pacing_gain * btlbw * rtprop` (floor: 4 * MSS) |
| Pacing Rate | `pacing_rate` | Derived | `pacing_gain * btlbw` |
| Pacing Gain | `pacing_gain` | `BBRStartupGain` | Varies by state/phase |
| Current RTT | `latest_rtt` | — | Per-ACK sample |
| Delivery Rate | `delivery_rate` | — | `delivered_bytes / interval` per ACK |

### 5.2 Constants

```
BBRStartupGain    = 2.89    // 2 / ln(2), exponential probing
BBRDrainGain      = 0.75    // Drain excess queue
BBRProbeBWGain    = 1.25    // Probe for more bw (phase 0)
BBRProbeBWDrain   = 0.75    // Drain after probe (phase 1)
BBRProbeBWCruise  = 1.00    // Cruise at estimated rate (phases 2-7)
BBRProbeRTTGain   = 1.00    // During PROBE_RTT, minimal inflight
BBRMinCwnd        = 4       // Minimum congestion window in packets
BBRProbeRTTInterval = 10 * time.Second
BBRProbeRTTDuration = 200 * time.Millisecond
BBRMinBwFilter    = 10      // ~10 RTTs for Btlbw max filter
BBRMinRTTFilter   = 10 * time.Second  // For RTprop min filter
BBRStartupFullBwCnt = 3     // 3 consecutive RTTs with <25% gain to exit STARTUP
```

### 5.3 State Machine

```
                         ┌──────────┐
                         │ STARTUP  │
                         │ gain=2.89│
                         └────┬─────┘
                              │ bandwidth plateaus (3 RTTs, <25% gain)
                              ▼
                         ┌──────────┐
                         │  DRAIN   │
                         │ gain=0.75│ (1 RTT)
                         └────┬─────┘
                              │ drain complete
                              ▼
                    ┌─────────────────────┐
                    │     PROBE_BW        │
                    │ 8-phase cycle:      │
                    │ [1.25, 0.75, 1,1,1, │
                    │   1,1,1] @ 1 RTT ea│
                    └──────────┬──────────┘
                               │
                    every 10s: RTT probe
                               │
                               ▼
                    ┌─────────────────────┐
                    │    PROBE_RTT        │
                    │ cwnd=4 pkts, 200ms │
                    └──────────┬──────────┘
                               │ probe done, rtprop updated
                               │
                               └──► back to PROBE_BW
```

### 5.4 State Behavior Detail

#### STARTUP
- **Pacing gain**: 2.89 (≈ 2/ln(2)) — exponential bandwidth discovery.
- **On each ACK**: update `btlbw` via windowed-max filter; update `rtprop` via windowed-min filter.
- **Exit condition**: If delivery rate does not increase by >25% for `BBRStartupFullBwCnt` (3) consecutive RTTs, bandwidth has plateaued → transition to DRAIN.
- **Goal**: Quickly find the bottleneck bandwidth by filling the pipe and the buffer, then exit before causing sustained loss.

#### DRAIN
- **Pacing gain**: 0.75 — intentionally below 1.0 to drain the standing queue built during STARTUP.
- **Duration**: One estimated RTT once inflight ≤ BDP (`btlbw * rtprop`).
- **Transition**: After inflight matches BDP → PROBE_BW.

#### PROBE_BW
- **Cycle**: 8 phases, each ~1 RTT.
  - Phase 0: pacing_gain = 1.25 (increase inflight to probe for more BW)
  - Phase 1: pacing_gain = 0.75 (drain any queue built during the probe)
  - Phases 2-7: pacing_gain = 1.00 (cruise at BDP)
- **On each ACK**: update `btlbw` and `rtprop` filters.
- **Transition to PROBE_RTT**: If ≥10 seconds since last PROBE_RTT and the RTprop estimate has not been updated (i.e., we may have a stale minimum RTT).

#### PROBE_RTT
- **Cwnd**: Reduced to `BBRMinCwnd` (4 packets) for `BBRProbeRTTDuration` (200ms).
- **Goal**: Drain all queues so we can sample the true base RTT (handles route changes where the new RTT is lower than our current estimate).
- **After 200ms**: Restore cwnd to previous value; `rtprop` filter resets with fresh sample window; return to PROBE_BW.
- **Cooldown**: Do not re-enter PROBE_RTT for `BBRProbeRTTInterval` (10s).

### 5.5 Interaction with RDT Sender

1. **Sending**: Before transmitting a DATA packet, the sender checks:
   ```
   if inflight >= min(cwnd, rwnd):
       block until space available
   ```
2. **Pacing**: Inter-packet gap = `payload_size / pacing_rate`. The sender uses a token-bucket or timer-based scheduler to enforce pacing.
3. **ACK processing**: On each ACK:
   - Compute `rtt_sample = now() - sent_timestamp[seq]`
   - Update BBR filters: `btlbw = windowed_max(delivery_rate)`, `rtprop = windowed_min(rtt_sample)`
   - Update `cwnd` and `pacing_rate` per current BBR state
   - Update `inflight = sent_bytes - delivered_bytes`
4. **Loss/Timeout**: BBR does NOT reduce cwnd on packet loss. Selective Repeat handles retransmission; BBR only adjusts bandwidth estimates from delivery rate samples.

---

## 6. Active (PORT) vs. Passive (PASV) Modes

### 6.1 PORT (Active Mode)

```
Client                              Server
  │                                    │
  │──PORT 192,168,1,100,200,10───────►│  Client tells server its IP + UDP port
  │◄──200 PORT command successful─────│
  │                                    │
  │──RETR file.txt───────────────────►│
  │◄──150 Opening data connection─────│
  │                                    │  Server initiates UDP to client's address
  │◄──UDP SYN─────────────────────────│  (server → client IP:port)
  │──UDP SYN+ACK────────────────────►│
  │◄──UDP ACK─────────────────────────│  3-way handshake complete
  │◄──UDP DATA(seq=1..N)──────────────│  Server pushes file data
  │──UDP ACK(seq=N)──────────────────►│
  │◄──UDP FIN─────────────────────────│
  │──UDP FIN+ACK────────────────────►│
  │◄──226 Transfer complete───────────│
```

**Rule**: PORT address is parsed as `h1.h2.h3.h4:p1*256+p2`. Server opens a UDP socket and connects to that address.

### 6.2 PASV (Passive Mode)

```
Client                              Server
  │                                    │
  │──PASV────────────────────────────►│
  │◄──227 Entering Passive Mode       │  Server allocates random UDP port
  │    (192,168,1,50,210,5)───────────│
  │                                    │
  │──RETR file.txt───────────────────►│
  │◄──150 Opening data connection─────│
  │                                    │  Client initiates UDP to server's port
  │──UDP SYN─────────────────────────►│  (client → server)
  │◄──UDP SYN+ACK─────────────────────│
  │──UDP ACK─────────────────────────►│  3-way handshake complete
  │◄──UDP DATA(seq=1..N)──────────────│  Server pushes file data
  │──UDP ACK(seq=N)──────────────────►│
  │◄──UDP FIN─────────────────────────│
  │──UDP FIN+ACK────────────────────►│
  │◄──226 Transfer complete───────────│
```

**Rule**: Server binds to `0.0.0.0:0` (OS-assigned random port), reports the allocated port in PASV response. Client parses the response and connects UDP to that address.

### 6.3 Sequence for STOR (Upload)

Same as RETR but direction reversed: data flows from client → server. The session initiator (who sends SYN first) follows the same pattern described above based on PORT vs PASV mode.

---

## 7. DATA Transfer Flow — Detailed

### 7.1 RETR (Download) — Server Sends File

```
Client (RDT Recv)               Server (RDT Send + FS)
       │                              │
       │◄──150 Opening data conn──────│
       │                              │
       │◄──UDP SYN────────────────────│
       │──UDP SYN+ACK────────────────►│
       │◄──UDP ACK────────────────────│
       │                              │
       │   [BBR: STARTUP gain=2.89]   │
       │◄──DATA(seq=0, len=1400)──────│  Read file chunk 0
       │──ACK(seq=0)─────────────────►│  Deliver to app, update rcv_base
       │◄──DATA(seq=1400, len=1400)───│  Chunk 1
       │──ACK(seq=1400)──────────────►│
       │     ... (Selective Repeat    │
       │      with per-packet timers) │
       │                              │
       │◄──DATA(seq=N, len=rem)───────│  Last chunk (< 1400 bytes signals EOF)
       │──ACK(seq=N)─────────────────►│
       │                              │
       │◄──UDP FIN────────────────────│
       │──UDP FIN+ACK────────────────►│
       │                              │
       │◄──226 Transfer complete──────│
       │                              │
       │──HASH remote_filename───────►│  Client requests SHA-256
       │◄──213 abcd1234...────────────│  Server computes and returns hash
       │                              │
       │  [Client computes local SHA-256, compares]
```

### 7.2 STOR (Upload) — Client Sends File

```
Client (RDT Send + FS)          Server (RDT Recv)
       │                              │
       │──STOR file.txt──────────────►│
       │◄──150 Opening data conn──────│
       │                              │
       │──UDP SYN────────────────────►│
       │◄──UDP SYN+ACK────────────────│
       │──UDP ACK────────────────────►│
       │                              │
       │──DATA(seq=0, len=1400)──────►│  Read file chunk 0
       │◄──ACK(seq=0)─────────────────│  Buffer and deliver
       │──DATA(seq=1400, len=1400)───►│
       │◄──ACK(seq=1400)──────────────│
       │     ...                      │
       │──DATA(seq=N, len=rem)───────►│
       │◄──ACK(seq=N)─────────────────│
       │                              │
       │──UDP FIN────────────────────►│
       │◄──UDP FIN+ACK────────────────│
       │                              │
       │◄──226 Transfer complete──────│
```

---

## 8. SHA-256 HASH Verification

### 8.1 Protocol

1. After transfer completes (both upload and download), either side may issue `HASH <filename>` on the control channel.
2. The server computes SHA-256 over the entire file on disk.
3. The server replies with `213 <64-char-hex-hash>` (file status + hash value).
4. The client computes SHA-256 over the local copy.
5. The client compares hashes and logs/reports the result (`MATCH` or `MISMATCH`).

### 8.2 Implementation Notes

- Use `crypto/sha256.Sum256()` for hashing.
- Read file in chunks (e.g., 64KB) to avoid loading entire file into memory.
- Hash computation is blocking; for large files, run in a goroutine with a timeout context.

---

## 9. Concurrency & Session Model

### 9.1 Server Architecture

```
┌──────────────────────────────────────────┐
│                  main()                  │
│  ┌──────────┐  ┌─────────────────────┐   │
│  │ TCP      │  │ Session Manager     │   │
│  │ Listener │  │ ┌─────────────────┐ │   │
│  │ :21      │──│ │ sessionRegistry │ │   │
│  └──────────┘  │ │ map[ID]*Session │ │   │
│                │ │ sync.RWMutex    │ │   │
│                │ └─────────────────┘ │   │
│                │ ┌─────────────────┐ │   │
│                │ │ context.Context │ │   │
│                │ │ cancelFunc      │ │   │
│                │ └─────────────────┘ │   │
│                └─────────────────────┘   │
└──────────────────────────────────────────┘

Per-Session Goroutine:
┌──────────────────────────────────────┐
│ handleSession(conn net.Conn)         │
│   ├── auth flow (USER/PASS)          │
│   ├── command loop (read → dispatch) │
│   ├── on RETR/STOR: spawn UDP        │
│   │   goroutine for RDT transfer     │
│   └── on QUIT: cleanup, cancel ctx   │
└──────────────────────────────────────┘
```

### 9.2 Session Lifecycle

1. `main()` starts TCP listener on `:21`.
2. `Accept()` returns a connection; spawns `handleSession()` goroutine.
3. `handleSession()` registers session in `sessionRegistry` with unique ID.
4. Authentication: USER → 331, PASS → 230 (or 530).
5. Command loop: read line, parse, dispatch to handler.
6. Data transfer: spawn separate goroutine for RDT communication; control channel remains responsive for ABOR and status commands.
7. QUIT: deregister session, close control conn, signal cleanup.

### 9.3 Client Architecture

```
┌──────────────────────────────────────┐
│ main()                               │
│   ├── Connect TCP to server :21      │
│   ├── CLI read-eval-print loop       │
│   │   ├── Parse user input           │
│   │   ├── Send command via TCP       │
│   │   ├── If transfer:               │
│   │   │   ├── Setup RDT over UDP     │
│   │   │   ├── Run BBR sender/recv    │
│   │   │   └── Update progress bar    │
│   │   └── Print response             │
│   └── On exit: QUIT                  │
└──────────────────────────────────────┘
```

---

## 10. CRC32 Checksum

### 10.1 Algorithm

- CRC32-IEEE (`IEEE` polynomial: `0xEDB88320`).
- Use `hash/crc32` standard library with `crc32.MakeTable(crc32.IEEE)`.
- Computed over: entire header (bytes 0-16) + entire payload.
- The Checksum field (bytes 17-20) is set to `0x00000000` during computation.
- Verifier recomputes CRC and compares to transmitted value; mismatch → silent discard.

### 10.2 Coverage Validation

```
compute_crc(packet):
    saved = packet[17:21]
    packet[17:21] = [0,0,0,0]
    crc = crc32.ChecksumIEEE(packet)
    packet[17:21] = saved
    return crc
```

---

## 11. Error Handling & Graceful Shutdown

### 11.1 Server Shutdown Sequence

1. Receive OS signal (`SIGINT` / `SIGTERM`).
2. Stop accepting new connections.
3. Broadcast `421 Service not available` to all active control sessions.
4. Cancel all session contexts.
5. Wait for all goroutines to finish (via `sync.WaitGroup`).
6. Close listener, exit.

### 11.2 RDT Abnormal Termination

- If 3 consecutive retransmissions of any packet fail → send RST on UDP, close data channel, report `426 Transfer aborted` on control channel.
- If UDP connection idle for >30 seconds with no ACK progress → RST + `426`.

---

## 12. Configuration Files

### 12.1 `users.conf` Format

```
# Hybrid FTP user credentials
# Format: username:SHA-256-hex-hash
# Passwords hashed with SHA-256 (no salt for demo simplicity)
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
```

### 12.2 Server Startup Flags

```
-server.port=21        # TCP control port
-server.dataport=0     # 0 = random per session (for PASV)
-server.root=/tmp/ftp  # Virtual filesystem root directory
-server.users=users.conf # Path to user config file
```

### 12.3 Client Startup Flags

```
-client.host=127.0.0.1 # Server host
-client.port=21        # Server control port
```

---

## 13. Key Design Decisions Summary

| Decision | Rationale |
|----------|-----------|
| No SACK/NACK | Per-packet timers in Selective Repeat reduce header complexity; ACK piggybacking sufficient |
| BBR over AIMD | Model-based CC avoids bufferbloat; produces better throughput/latency in demo scenarios; showcases advanced understanding |
| CRC32 over the entire packet | Catches both header and payload corruption; standard library support; faster than SHA-256 for per-packet checks |
| Session ID in RDT header | Enables single UDP socket for server to demultiplex multiple concurrent client sessions |
| 1400-byte max payload | Safest max under 1500 MTU accounting for all headers; widely compatible |
| STOU format | UUID_timestamp_originalname ensures uniqueness across concurrent uploads |
