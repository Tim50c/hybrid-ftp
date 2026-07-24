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

### Manual Testing

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

#### 3. Login and Navigation

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

#### 4. Directory Operations

```
ftp> MKD newdir
257 "/newdir" created.
ftp> RMD newdir
250 "/newdir" removed.
ftp> LIST
227 Entering Passive Mode (127,0,0,1,xxx,xxx)
150 Opening data connection for directory listing.
226 Directory listing complete.
```

#### 5. Download a File (RETR)

```
ftp> PASV
227 Entering Passive Mode (127,0,0,1,xxx,xxx)
ftp> RETR hello.txt
150 Opening data connection for hello.txt (21 bytes).
██████████████████████████████ 100% | 2.0 MiB/s | ETA: 00:00
226 Transfer complete.
```

#### 6. Upload a File (STOR)

First, create a local file:
```bash
# In the client directory
echo "uploaded from client" > upload.txt
```

```
ftp> PASV
227 Entering Passive Mode (127,0,0,1,xxx,xxx)
ftp> STOR upload.txt
150 Opening data connection for upload.txt.
██████████████████████████████ 100% | 1.8 MiB/s | ETA: 00:00
226 Transfer complete.
```

#### 7. Hash Verification

```
ftp> HASH hello.txt
213 5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03
Hashes match - file integrity verified.
```

#### 8. PORT Mode (Active)

```
ftp> PORT 127,0,0,1,200,0
200 PORT command successful.
ftp> RETR hello.txt
150 Opening data connection for hello.txt (21 bytes).
██████████████████████████████ 100% | 2.0 MiB/s | ETA: 00:00
226 Transfer complete.
```

#### 9. Large File Transfer (Progress Bar)

```bash
# Create a test file
dd if=/dev/urandom of=/tmp/hybrid-ftp/bigfile.bin bs=1M count=50
```

```
ftp> PASV
ftp> RETR bigfile.bin
████████████████████████░░░░░░░░ 65% | 3.5 MiB/s | ETA: 00:05
```

#### 10. Concurrent Sessions

Open two terminals and run the client in each. Both can authenticate and transfer files simultaneously — each session gets its own UDP data port, and transfers proceed independently.

#### 11. Local Client Commands

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
│       └── protocol.go       # TCP line read/write helpers
├── test/
│   ├── integration/e2e_test.go  # End-to-end integration tests
│   └── fixtures/sample.txt
├── users.conf                # SHA-256 hashed user credentials
├── go.mod
└── AGENTS.md                 # Developer reference + conventions
```
