# Basic Level Design — Hybrid FTP

Date: 2025-07-22
Status: Approved

## 1. Scope

| Component | Full Spec | Basic Level |
|-----------|-----------|-------------|
| Congestion control | BBR v1 (4 states) | **None** — fixed cwnd = 20 * 1400 = 28000 bytes |
| Data channel mode | PORT + PASV | **PASV only** (server opens random UDP port, client connects) |
| Transfer type | ASCII + Binary | **ASCII only** (TYPE A) |
| Commands | 27 commands | **10 core**: USER, PASS, QUIT, TYPE, PASV, RETR, STOR, PWD, LIST, CWD |
| File ops | Full CRUD + HASH + STOU + APPE + RNFR/RNTO | **RETR + STOR only** (single file upload/download) |
| ARQ | Selective Repeat + per-packet timers | **Full Selective Repeat** (identical to spec) |
| RDT packet header | 21 bytes with CRC32 | **Full implementation** (identical to spec) |

## 2. Architecture Diagram

```
Client                           Server
  |--TCP connect :21------------>|
  |<--220 Ready------------------|
  |--USER admin------------------>|
  |<--331 Password required------|
  |--PASS admin------------------>|  (SHA-256 verified against users.conf)
  |<--230 Logged in--------------|
  |--PASV------------------------>|
  |<--227 (h1,h2,h3,h4,p1,p2)---|  Server binds UDP socket at :random
  |--RETR file.txt-------------->|
  |<--150 Opening data-----------|
  |--UDP SYN-------------------->|  Client initiates UDP connection (PASV)
  |<--UDP SYN+ACK----------------|
  |--UDP ACK-------------------->|  3-way handshake complete
  |<--UDP DATA(seq=0..N)---------|  Server pushes file in 1400-byte chunks
  |--UDP ACK(seq=N)------------->|  Selective Repeat: per-packet ACKs
  |<--UDP FIN--------------------|
  |--UDP FIN+ACK---------------->|
  |<--226 Transfer complete------|
  |--QUIT------------------------>|
  |<--221 Goodbye----------------|
```

## 3. Data Structures

### 3.1 Session (`internal/session/manager.go`)

```go
type Session struct {
    ID            uint32
    Conn          net.Conn        // TCP control connection
    Username      string
    Authenticated bool
    CWD           string          // current working directory
    DataConn      *net.UDPConn    // bound UDP socket (PASV)
    TransferType  byte            // 'A' (ASCII) or 'I' (Binary)
    Ctx           context.Context
    Cancel        context.CancelFunc
}

type Manager struct {
    mu       sync.RWMutex
    registry map[uint32]*Session
    nextID   uint32
}
```

### 3.2 RDT Header (`internal/rdt/header.go`)

21 bytes, binary-encoded:

| Offset | Size | Field | Type |
|--------|------|-------|------|
| 0 | 4 | SessionID | uint32 BE |
| 4 | 4 | SeqNum | uint32 BE |
| 8 | 4 | AckNum | uint32 BE |
| 12 | 1 | Flags | uint8 |
| 13 | 2 | PayloadLen | uint16 BE |
| 15 | 2 | Window | uint16 BE |
| 17 | 4 | Checksum | uint32 BE |

Flags: SYN=0x01, FIN=0x02, ACK=0x04, RST=0x08, DATA=0x10

Maximum payload: 1400 bytes per datagram.

### 3.3 RDT Sender (`internal/rdt/sender.go`)

Pushes application data over UDP with Selective Repeat. Supports both initiating the connection (`Connect`) and waiting for an incoming connection (`Accept`).

```go
type Sender struct {
    conn       *net.UDPConn
    sessionID  uint32
    remoteAddr *net.UDPAddr
    state      senderState     // CLOSED, SYN_SENT, ESTABLISHED, FIN_SENT
    sendBase   uint32
    nextSeqNum uint32
    windowSize uint32          // fixed: 28000 (20 * 1400)
    recvWindow uint16          // latest receiver-advertised window
    inflight   map[uint32]*packetRecord
    timers     *TimerManager
    outbox     chan []byte
}

// Methods:
func NewSender(conn *net.UDPConn, sessionID uint32) *Sender
func (s *Sender) Connect(addr *net.UDPAddr) error    // initiate: SYN → wait SYN+ACK → ACK
func (s *Sender) Accept() error                       // wait: rcv SYN → SYN+ACK → rcv ACK
func (s *Sender) Send(data []byte) error              // queue data, send via window
func (s *Sender) Close() error                        // FIN handshake
```

### 3.4 RDT Receiver (`internal/rdt/receiver.go`)

Receives application data over UDP with reorder buffer. Supports both initiating and accepting.

```go
type Receiver struct {
    conn       *net.UDPConn
    sessionID  uint32
    remoteAddr *net.UDPAddr
    state      receiverState   // CLOSED, LISTENING, ESTABLISHED, RECEIVING
    rcvBase    uint32
    recvBuffer map[uint32][]byte
    recvWindow uint16          // 65535
    output     chan []byte
}

// Methods:
func NewReceiver(conn *net.UDPConn, sessionID uint32) *Receiver
func (r *Receiver) Connect(addr *net.UDPAddr) error   // initiate: SYN → wait SYN+ACK → ACK
func (r *Receiver) Accept() error                      // wait: rcv SYN → SYN+ACK → rcv ACK
func (r *Receiver) Receive() <-chan []byte             // channel of in-order delivered data
func (r *Receiver) Close() error                       // FIN handshake
```

### 3.5 Timer Manager (`internal/rdt/timer.go`)

```go
type TimerManager struct {
    mu       sync.Mutex
    timers   map[uint32]*time.Timer
    backoff  map[uint32]int
    timeout  time.Duration     // base: 500ms
    onExpire func(seq uint32)
}
```

Backoff: doubles per retransmit for that seq, capped at 60s. After 3 consecutive timeouts for same packet → RST + error.

### 3.6 Commands (`internal/tcpctrl/commands.go`)

```go
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
```

### 3.7 Reply Codes (`internal/tcpctrl/replies.go`)

| Code | Constant | Meaning |
|------|----------|---------|
| 150 | ReplyFileOK | Opening data connection |
| 200 | ReplyOK | Command okay |
| 220 | ReplyReady | Service ready |
| 221 | ReplyQuit | Goodbye |
| 226 | ReplyClosingData | Transfer complete |
| 227 | ReplyPASV | Entering passive mode |
| 230 | ReplyLoggedIn | Login successful |
| 250 | ReplyFileActionOK | Requested file action okay (CWD, TYPE) |
| 257 | ReplyPathCreated | Pathname (PWD response) |
| 331 | ReplyNeedPassword | Send password |
| 425 | ReplyCantOpenData | Cannot open data connection |
| 426 | ReplyTransferAborted | Transfer aborted |
| 500 | ReplySyntaxError | Unknown command |
| 503 | ReplyBadSequence | Bad command sequence |
| 504 | ReplyNotImpl | Not implemented |
| 530 | ReplyNotLoggedIn | Not logged in |
| 550 | ReplyFileUnavail | File unavailable |

### 3.8 Auth (`internal/auth/auth.go`)

```go
func CheckPassword(username, password string) bool
```

Reads `users.conf`, hashes password with SHA-256, compares to stored hex hash.

### 3.9 File System Ops (`internal/fs/ops.go`)

```go
func ResolvePath(session *session.Session, path string) string
func ListDir(dirPath string) ([]os.FileInfo, error)
func OpenForRead(filePath string) (*os.File, os.FileInfo, error)
func OpenForWrite(filePath string) (*os.File, error)
```

## 4. Server Command Dispatch Logic

```
handleSession(conn):
    Send 220 Ready
    Loop:
        ReadLine → ParseCommand → switch:
            USER: if already authenticated→503; else store username, reply 331
            PASS: if not after USER→503; else check auth, reply 230/530
            QUIT: reply 221, close, return
            (all below: if not authenticated→530)
            TYPE: store 'A'/'I' in session, reply 200
            PASV: bind UDP :0, store DataConn in session, reply 227
            RETR: OpenForRead, reply 150, Accept RDT sender, stream file, reply 226
            STOR: OpenForWrite, reply 150, Accept RDT receiver, write file, reply 226
            PWD:  reply 257 with CWD
            CWD:  validate path exists, update CWD, reply 250
            LIST: resolve path, list directory, reply 150, send listing via RDT, reply 226
            default: reply 500
```

## 5. RDT Connection Flows

In PASV mode, the **client always initiates** the UDP connection (sends SYN first), regardless of which direction the file data flows. Both sides instantiate an RDT connection that can send and/or receive after handshake.

### 5.1 RETR (download):

| Step | Client | Server |
|------|--------|--------|
| 1 | Sends PASV via TCP | Binds UDP on random port, replies 227 |
| 2 | Sends RETR via TCP | Replies 150 |
| 3 | Opens UDP socket, sends SYN to server's port | Accepts SYN, sends SYN+ACK |
| 4 | Sends ACK | Handshake complete |
| 5 | **Receives** file data (RDT receiver role) | **Sends** file data in 1400-byte chunks (RDT sender role) |
| 6 | ACKs each received DATA packet | Retransmits on timeout, slides send window |
| 7 | Receives FIN, sends FIN+ACK | Sends FIN after all data |
| 8 | Reads 226 on TCP | Sends 226 |

### 5.2 STOR (upload):

| Step | Client | Server |
|------|--------|--------|
| 1 | Sends PASV via TCP | Binds UDP on random port, replies 227 |
| 2 | Sends STOR via TCP | Replies 150 |
| 3 | Opens UDP socket, sends SYN to server's port | Accepts SYN, sends SYN+ACK |
| 4 | Sends ACK | Handshake complete |
| 5 | **Sends** file data in 1400-byte chunks (RDT sender role) | **Receives** file data (RDT receiver role) |
| 6 | Retransmits on timeout, slides send window | ACKs each received DATA packet |
| 7 | Sends FIN after all data | Sends FIN+ACK |
| 8 | Reads 226 on TCP | Sends 226 |

**Key design**: The RDT Sender and Receiver are data-direction roles, not connection-initiation roles. Each side instantiates both a Sender (for pushing data) and Receiver (for accepting data) as needed per transfer direction. The UDP handshake initiator is always the client in PASV mode.

### 5.3 Selective Repeat Behavior

- **Sender**: window = min(28000, rwnd). Each packet in inflight has its own timer. On ACK: cancel timer, advance sendBase to lowest unacked seq. On timer expiry: retransmit, double timeout.
- **Receiver**: In-order packets delivered immediately to output channel, rcvBase advances. Out-of-order packets buffered in recvBuffer. ACK sent for every received DATA packet (no delayed ACK). Window field advertises remaining buffer.
- **Checksum**: CRC32-IEEE over header + payload. Failing packets silently discarded.

## 6. Sequence Numbers

Byte-oriented: SeqNum advances by PayloadLen per DATA packet. Last chunk identified by PayloadLen < 1400 (or zero-length DATA). Receiver reassembles by writing payloads at offset seqNum in a growing buffer, delivering contiguous in-order bytes.

## 7. Error Handling

| Condition | Control Channel | Data Channel |
|-----------|----------------|-------------|
| Invalid user/password | 530 Not logged in | — |
| File not found (RETR) | 550 File unavailable | — |
| Write error (STOR) | 550 File unavailable | — |
| Checksum mismatch | — | Silent discard, no ACK → sender retransmits |
| 3 consecutive retransmit timeouts | 426 Transfer aborted | RST on UDP |
| Unknown command | 500 Syntax error | — |
| RETR/STOR before PASV | 503 Bad sequence | — |
| Multiple USER commands when authenticated | 503 Bad sequence | — |

## 8. Selective Repeat without BBR

- cwnd fixed at 28000 bytes (20 packets)
- No pacing rate calculation — simple inter-packet delay of 0 (send as fast as window allows)
- No delivery rate estimation or RTT filtering
- Inflight cap: `min(28000, receiver_advertised_window)`
- Block on send when inflight >= cap

## 9. Configuration

### 9.1 users.conf

```
# Format: username:SHA-256-hex-hash
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
```

Passwords: `admin`/`user` respectively (SHA-256 hashed).

## 10. Directory Structure (new files)

```
internal/
├── auth/auth.go
│   └── auth_test.go
├── tcpctrl/
│   ├── commands.go
│   ├── commands_test.go
│   ├── replies.go
│   ├── replies_test.go
│   └── protocol.go
├── session/
│   └── manager.go
├── fs/
│   └── ops.go
└── rdt/
    ├── header.go
    ├── header_test.go
    ├── checksum.go
    ├── checksum_test.go
    ├── timer.go
    ├── timer_test.go
    ├── sender.go
    └── receiver.go
cmd/
├── server/main.go
└── client/main.go
test/
├── fixtures/sample.txt
└── integration/e2e_test.go
users.conf
```
