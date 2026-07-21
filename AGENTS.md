# AGENTS.md — Hybrid FTP Project

> **CRITICAL**: All downstream AI coding agents (implementers, testers, documenters) MUST read this file before modifying any source code. This is the single source of truth for the project.

---

## Project Overview

Hybrid FTP decouples the FTP control plane (TCP) from the data plane (Custom Reliable UDP).
**Language**: Go (golang) — standard library only.
**Third-party libraries**: NONE. No KCP, QUIC, libcurl, or any external networking frameworks.

---

## System Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      Client                              │
│  ┌─────────────┐              ┌───────────────────────┐  │
│  │ TCP Control │──TCP:21────►│    TCP Control         │  │
│  │ CLI Handler │◄──responses─│    Server               │  │
│  └─────────────┘              └───────┬───────────────┘  │
│  ┌─────────────┐                      │                  │
│  │ RDT Data    │──UDP:data──►│    RDT Data            │  │
│  │ Sender/Recv │◄──UDP:data──│    Sender/Receiver     │  │
│  └─────────────┘              └───────────────────────┘  │
└──────────────────────────────────────────────────────────┘
```

- **Control Channel**: TCP, handles all FTP commands (USER, PASS, RETR, STOR, LIST, etc.) and reply codes.
- **Data Channel**: Custom RDT over UDP, handles file transfer payload with Selective Repeat ARQ and BBR congestion control.
- Both channels multiplexed per session via a Session ID in the RDT header.

---

## Key Architecture Decisions

| Decision | Choice |
|----------|--------|
| ARQ Mechanism | Selective Repeat (per-packet timers, individual ACKs, no NACK, no SACK) |
| Congestion Control | BBR v1 (Bottleneck Bandwidth and RTT) — 4 states: STARTUP, DRAIN, PROBE_BW, PROBE_RTT |
| Hash Algorithm | SHA-256 (`crypto/sha256`) |
| Password Storage | SHA-256 hashed (not plaintext) |
| Active/Passive | Mirrors RFC 959: PORT=server initiates UDP to client, PASV=server opens random UDP port |
| Concurrency | Goroutine-per-session + `sync.RWMutex` session registry |
| Progress Reporting | ANSI terminal progress bar |
| MODE B/C | Return `504 Not implemented.`; real implementation deferred to project completion |

---

## Hard Rules for All Agents

1. **NO external libraries** for networking, FTP, or reliable transport. `import` statements must be from: `net`, `crypto`, `os`, `fmt`, `io`, `bufio`, `time`, `sync`, `strings`, `strconv`, `encoding/binary`, `hash`, `log`, `flag`, `path/filepath`, `errors`, `context`, `unicode`, `unicode/utf8`.
2. **Error handling**: Every error must be checked. Use `fmt.Errorf("context: %w", err)` for wrapping. Never `panic()` in library code — only in `main()` for irrecoverable startup failures.
3. **Logging**: Use `log.Printf()` with consistent prefixes: `[CTRL]`, `[RDT]`, `[FS]`, `[SESS]`, `[AUTH]`.
4. **Concurrency safety**: All shared maps protected by `sync.RWMutex`. Channels used for cross-goroutine signaling. Context cancellation for graceful shutdown.
5. **No code generation without reading**: Before writing any file, read the existing code in the target package first to match conventions.
6. **Test files**: Unit tests in `*_test.go` alongside source. Integration tests in `test/` directory.
7. **Commit messages**: Format as `[scope] brief description`. Examples: `[ctrl] add PORT command parsing`, `[rdt] implement BBR PROBE_BW state`.

---

## Task Assignment Matrix

| Component | Owner | Module Path |
|-----------|-------|-------------|
| TCP Control Protocol (command parser, reply codes) | Developer A (Server) | `internal/tcpctrl/` |
| Server main, session manager, authentication | Developer A (Server) | `cmd/server/`, `internal/session/`, `internal/auth/` |
| File system operations (LIST, MKD, STOR, RETR, etc.) | Developer A (Server) | `internal/fs/` |
| Client CLI, command builder, progress bar | Developer B (Client) | `cmd/client/` |
| RDT Protocol (header, state machine, timers, checksum) | Developer B (Client) | `internal/rdt/` |
| BBR Congestion Control | Developer B (Client) | `internal/rdt/bbr.go` |
| Active/Passive mode UDP setup | Both (shared) | `internal/rdt/` + `internal/tcpctrl/` |
| HASH command (SHA-256) | Both (shared) | `internal/fs/` + `internal/rdt/` |
| Integration tests | Both | `test/` |
| Technical Report & GenAI Log | Both | `docs/` |

---

## Directory Structure

```
hybrid-ftp/
├── cmd/
│   ├── server/main.go
│   └── client/main.go
├── internal/
│   ├── tcpctrl/          # Control channel protocol (shared types, command parsing)
│   │   ├── commands.go    # Command enum and parser
│   │   ├── replies.go     # Reply codes and formatters
│   │   └── protocol.go    # TCP connection handler (shared)
│   ├── rdt/               # Custom Reliable Data Transfer over UDP
│   │   ├── header.go      # Packet header encode/decode
│   │   ├── sender.go      # RDT Sender state machine + Selective Repeat
│   │   ├── receiver.go    # RDT Receiver state machine
│   │   ├── timer.go       # Per-packet retransmit timers
│   │   ├── checksum.go    # CRC32-IEEE computation
│   │   └── bbr.go         # BBR congestion control
│   ├── fs/                # File system operations
│   │   └── ops.go         # LIST, MKD, RMD, DELE, STOR, RETR, RNFR/RNTO, HASH
│   ├── session/           # Session management
│   │   └── manager.go     # Session registry, goroutine lifecycle
│   └── auth/              # Authentication
│       └── auth.go        # USER/PASS with SHA-256 hashed credentials
├── docs/
│   ├── ARCHITECTURE.md
│   ├── TASKS.md
│   └── GENAI_LOG.md
├── test/
│   ├── integration/
│   │   └── e2e_test.go
│   └── fixtures/
│       └── sample.txt
├── go.mod
├── users.conf             # User credentials (SHA-256 hashed)
└── AGENTS.md
```

---

## RDT Header Specification (21 bytes)

| Offset | Size (bytes) | Field | Type | Description |
|--------|-------------|-------|------|-------------|
| 0 | 4 | Session ID | uint32 | Demultiplexes UDP packets per client session |
| 4 | 4 | Sequence Number | uint32 | DATA/SYN/FIN: packet position in stream. ACK-only: 0. |
| 8 | 4 | ACK Number | uint32 | Piggybacked ACK: latest received seq number |
| 12 | 1 | Flags | uint8 | Bit flags (see below) |
| 13 | 2 | Payload Length | uint16 | Bytes in payload (0 for pure-ACK/SYN/FIN/RST) |
| 15 | 2 | Window Size | uint16 | Receiver-advertised buffer window in bytes |
| 17 | 4 | Checksum | uint32 | CRC32-IEEE over entire packet (header + payload) |

### Flag Bits (uint8)

| Bit | Mask | Name | Meaning |
|-----|------|------|---------|
| 0 | 0x01 | SYN | Connection initiation |
| 1 | 0x02 | FIN | Graceful close |
| 2 | 0x04 | ACK | Acknowledgment (ACK Number is valid) |
| 3 | 0x08 | RST | Abrupt reset |
| 4 | 0x10 | DATA | Payload follows header |
| 5-7 | — | — | Reserved (set to 0) |

---

## BBR Congestion Control Overview

BBR (Bottleneck Bandwidth and RTT) is a **model-based** congestion controller. It continuously estimates two path parameters:

| Parameter | Symbol | Window | Update Rule |
|-----------|--------|--------|-------------|
| Bottleneck Bandwidth | Btlbw | Max delivery rate over last ~10 RTTs | Windowed max of `delivered_bytes / interval` per ACK |
| Round-trip Propagation Time | RTprop | Min RTT over last ~10 seconds | Windowed min of RTT samples |

**Core invariant**: inflight data <= Btlbw * RTprop (the Bandwidth-Delay Product, BDP).

### BBR State Machine (4 states)

1. **STARTUP** — Exponential bandwidth probing. Pacing gain = 2.89 (≈ 2/ln(2)). Exits when estimated bandwidth plateaus for 3 consecutive RTTs (< 25% bandwidth increase).

2. **DRAIN** — After STARTUP, drains the queue built during startup. Pacing gain ≈ 0.75. Lasts one estimated RTT.

3. **PROBE_BW** — Steady-state cyclic probing (8 phases, ~1 RTT each):
   - Phase 0: pacing_gain = 1.25 (probe for more bandwidth)
   - Phase 1: pacing_gain = 0.75 (drain excess queue)
   - Phases 2-7: pacing_gain = 1.0 (cruise at estimated rate)

4. **PROBE_RTT** — Every 10 seconds, reduces inflight to ~4 packets for ~200ms to probe for a new minimum RTT (handles route changes).

**Interaction with Selective Repeat**:
- BBR sets `cwnd = pacing_gain * Btlbw * RTprop` (floor: 4 packets)
- RDT sender obeys `cwnd` — never more than `cwnd` bytes in flight
- Token-bucket pacing spaces packet transmissions at `pacing_rate = pacing_gain * Btlbw`
- RTT samples from per-packet ACK timestamps
- Delivery rate estimated from bytes delivered per RTT interval
