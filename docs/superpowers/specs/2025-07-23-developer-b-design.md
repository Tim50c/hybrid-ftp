# Developer B — Design Specification

> **Date:** 2025-07-23
> **Target modules:** RDT congestion control (BBR), client-side progress bar, PORT mode, HASH command, sender integration
> **Assumes:** Developer A's server code (auth, session, tcpctrl, fs, RDT receiver/sender, cmd/server) is complete per current codebase

---

## 1. BBR Congestion Control (`internal/rdt/bbr.go`)

### 1.1 Struct & Constants

```go
type bbrState int

const (
    bbrStartup bbrState = iota
    bbrDrain
    bbrProbeBW
    bbrProbeRTT
)

const (
    MSS                 = 1400
    bbrStartupGain      = 2.89
    bbrDrainGain        = 0.75
    bbrProbeBWGainUp    = 1.25
    bbrProbeBWGainDown  = 0.75
    bbrProbeBWGainSteady = 1.0
    bbrStartupFullBwCnt = 3
    bbrProbeRTTInterval = 10 * time.Second
    bbrProbeRTTDuration = 200 * time.Millisecond
    bbrMinCwnd          = 4 * MSS      // 5600 bytes
    bbrBwFilterLen      = 10
)

type BBR struct {
    mu              sync.Mutex
    state           bbrState
    btlbw           float64           // bottleneck bandwidth (bytes/sec)
    rtprop          time.Duration     // round-trip propagation time
    cwnd            uint32            // congestion window in bytes
    pacingRate      float64           // bytes/sec
    pacingGain      float64

    delivered       uint64            // cumulative bytes ACKed
    inflightBytes   uint32            // BBR-internal inflight counter
    latestRTT       time.Duration

    // STARTUP tracking
    fullBwCount     int
    lastDelivered   uint64
    lastCheckTime   time.Time

    // PROBE_BW cycling
    phase           int               // 0..7
    phaseStart      time.Time

    // PROBE_RTT
    lastProbeRTT    time.Time
    inProbeRTT      bool
    probeRTTEnd     time.Time

    // RTT-boundary bandwidth filter (max over 10 RTT rounds)
    bwWindow        [bbrBwFilterLen]float64
    bwWindowIdx     int
    roundBw         float64           // running max bw for current round
    nextRoundDelivered uint64         // delivered count at next round boundary
    roundStart      bool              // true when starting a new round

    // RTT min filter (10-second window)
    rttSamples      []rttSample
}

type rttSample struct {
    rtt time.Duration
    at  time.Time
}
```

### 1.2 Public Interface

```go
func NewBBR() *BBR

// OnACK: delivery rate = (b.delivered - deliveredAtSend) / time.Since(sentAt)
// Updates btlbw (max across RTT rounds), rtprop (min over 10s)
func (b *BBR) OnACK(ackedBytes uint32, rtt time.Duration, sentAt time.Time, deliveredAtSend uint64)

// OnSend: increment inflight tracker
func (b *BBR) OnSend(dataLen uint32)

// OnLoss: decrement inflight for unrecovered packet loss (prevents CanSend deadlock)
func (b *BBR) OnLoss(lostBytes uint32)

// CanSend: inflight + dataLen + dataLen <= cwnd && <= recvWindow
func (b *BBR) CanSend(dataLen uint32, recvWindow uint16) bool

// PacingDelay: dataLen / pacingRate (used with select, never time.Sleep directly)
func (b *BBR) PacingDelay(dataLen uint32) time.Duration

// GetDelivered: returns current delivered count (snapshot for packetRecord)
func (b *BBR) GetDelivered() uint64
```

### 1.3 State Machine

```
STARTUP (gain=2.89)
    │
    │ bandwidth plateaus 3 RTTs (<25% growth)
    ▼
DRAIN (gain=0.75)
    │
    │ inflight <= BDP (= btlbw * rtprop)
    ▼
PROBE_BW (gain cycles [1.25, 0.75, 1.0,1.0,1.0,1.0,1.0,1.0] every RTT)
    │                                    ▲
    │ 10s since last probe               │ after 200ms restore cwnd
    ▼                                    │
PROBE_RTT (cwnd = 4*MSS for 200ms) ─────┘
```

### 1.4 OnACK Core Logic

```go
func (b *BBR) OnACK(ackedBytes uint32, rtt time.Duration, sentAt time.Time, deliveredAtSend uint64) {
    b.mu.Lock()
    defer b.mu.Unlock()

    now := time.Now()
    b.delivered += uint64(ackedBytes)
    b.inflightBytes -= uint64(ackedBytes) // Note: handle underflow by flooring at 0
    b.latestRTT = rtt

    // Update RTT min filter (10-second window)
    b.rttSamples = append(b.rttSamples, rttSample{rtt, now})
    b.trimRTTSamples(now)
    b.rtprop = b.minRTTFromSamples()

    // Delivery rate estimate
    deliveryRate := float64(b.delivered - deliveredAtSend) / time.Since(sentAt).Seconds()

    // RTT-boundary bandwidth update
    if b.roundStart {
        b.nextRoundDelivered = b.delivered
        b.roundStart = false
    }
    if b.delivered >= b.nextRoundDelivered {
        b.bwWindow[b.bwWindowIdx%uint32(bbrBwFilterLen)] = b.roundBw
        b.bwWindowIdx++
        b.roundBw = deliveryRate
        b.nextRoundDelivered = b.delivered
    } else {
        if deliveryRate > b.roundBw {
            b.roundBw = deliveryRate
        }
    }
    b.btlbw = b.maxBwFromFilter()

    // State-specific logic
    switch b.state {
    case bbrStartup:
        b.handleStartup(deliveryRate, now)
    case bbrDrain:
        b.handleDrain()
    case bbrProbeBW:
        b.handleProbeBW(now)
    case bbrProbeRTT:
        b.handleProbeRTT(now)
    }

    // Update cwnd and pacing rate
    b.updateCwndAndRate()
}

func (b *BBR) updateCwndAndRate() {
    bdp := b.pacingGain * b.btlbw * float64(b.rtprop.Seconds())
    b.cwnd = uint32(max(bdp, float64(bbrMinCwnd)))
    b.pacingRate = b.pacingGain * b.btlbw
}
```

### 1.5 State Transitions

**STARTUP → DRAIN:**
```go
func (b *BBR) handleStartup(deliveryRate float64, now time.Time) {
    if deliveryRate > b.lastRate*1.25 {
        b.fullBwCount = 0
    }
    // Advance check per RTT
    if now.Sub(b.lastCheckTime) >= b.rtprop {
        b.lastCheckTime = now
        b.fullBwCount++
        b.lastRate = deliveryRate
    }
    if b.fullBwCount >= bbrStartupFullBwCnt {
        b.state = bbrDrain
        b.pacingGain = bbrDrainGain
    }
}
```

**DRAIN → PROBE_BW:**
```go
func (b *BBR) handleDrain() {
    bdp := b.btlbw * float64(b.rtprop.Seconds())
    if float64(b.inflightBytes) <= bdp {
        b.state = bbrProbeBW
        b.pacingGain = bbrProbeBWGainSteady
        b.phase = 0
        b.phaseStart = time.Now()
    }
}
```

**PROBE_BW phase cycling:**
```go
func (b *BBR) handleProbeBW(now time.Time) {
    if now.Sub(b.phaseStart) >= b.rtprop {
        b.phase = (b.phase + 1) % 8
        b.phaseStart = now
    }
    switch b.phase {
    case 0:
        b.pacingGain = bbrProbeBWGainUp
    case 1:
        b.pacingGain = bbrProbeBWGainDown
    default:
        b.pacingGain = bbrProbeBWGainSteady
    }

    if now.Sub(b.lastProbeRTT) >= bbrProbeRTTInterval {
        b.lastProbeRTT = now
        b.state = bbrProbeRTT
        b.inProbeRTT = true
        b.probeRTTEnd = now.Add(bbrProbeRTTDuration)
        b.cwnd = bbrMinCwnd
    }
}
```

**PROBE_RTT → PROBE_BW:**
```go
func (b *BBR) handleProbeRTT(now time.Time) {
    if now.After(b.probeRTTEnd) {
        b.inProbeRTT = false
        b.state = bbrProbeBW
        b.rttSamples = b.rttSamples[:0] // reset min RTT filter
        b.lastProbeRTT = time.Now()
    }
}
```

### 1.6 Filter Helpers

```go
func (b *BBR) trimRTTSamples(now time.Time) {
    cutoff := now.Add(-RTTFilterDuration)
    i := 0
    for _, s := range b.rttSamples {
        if s.at.After(cutoff) {
            b.rttSamples[i] = s
            i++
        }
    }
    b.rttSamples = b.rttSamples[:i]
}

func (b *BBR) minRTTFromSamples() time.Duration {
    if len(b.rttSamples) == 0 {
        return time.Hour
    }
    minRTT := b.rttSamples[0].rtt
    for _, s := range b.rttSamples[1:] {
        if s.rtt < minRTT {
            minRTT = s.rtt
        }
    }
    return minRTT
}

func (b *BBR) maxBwFromFilter() float64 {
    maxBw := 0.0
    for _, bw := range b.bwWindow {
        if bw > maxBw {
            maxBw = bw
        }
    }
    return maxBw
}
```

### 1.7 Unit Test Requirements

- [ ] **Test NewBBR Init**: Verify defaults — state = STARTUP, cwnd = bbrMinCwnd, pacingGain = 2.89
- [ ] **Test OnSend/OnLoss/CanSend**: inflight tracking, loss decrement, window gating
- [ ] **Test STARTUP→DRAIN transition**: Simulate 3 RTTs with flat bandwidth → state = DRAIN
- [ ] **Test DRAIN→PROBE_BW**: Inflight drops below BDP → state = PROBE_BW
- [ ] **Test PROBE_BW phase cycling**: Gains cycle correctly through 8 phases
- [ ] **Test PROBE_RTT entry**: After 10s, state = PROBE_RTT, cwnd = 4*MSS
- [ ] **Test PROBE_RTT exit**: After 200ms, state = PROBE_BW, cwnd restored
- [ ] **Test RTT boundary filter**: Bandwidth max across RTT rounds
- [ ] **Test RTT min filter**: 10-second window with trimming
- [ ] **Test delivery rate calculation**: Correct from (delivered - deliveredAtSend) / duration
- [ ] **Test cwnd/pacing rate update**: Correct formula output

---

## 2. Sender BBR Integration (`internal/rdt/sender.go`)

### 2.1 packetRecord Addition

```go
type packetRecord struct {
    data             []byte
    seq              uint32
    length           uint32
    sentAt           time.Time
    retransCount     int
    done             bool
    deliveredAtSend  uint64   // NEW
}
```

### 2.2 Sender Struct Changes

```go
type Sender struct {
    conn       *net.UDPConn
    sessionID  uint32
    remoteAddr *net.UDPAddr
    mu         sync.Mutex
    state      senderState
    sendBase   uint32
    nextSeqNum uint32
    // REMOVED: windowSize uint32
    recvWindow uint16
    inflight   map[uint32]*packetRecord
    timers     *TimerManager
    bbr        *BBR              // NEW
    outbox     chan []byte
    ackCh      chan ackEvent
    done       chan struct{}
    errCh      chan error
}
```

### 2.3 Removed

- Constant `const fixedCWND = 28000`
- Field `windowSize uint32`
- Method `effectiveWindow()`

### 2.4 NewSender

```go
func NewSender(conn *net.UDPConn, sessionID uint32) *Sender {
    s := &Sender{
        conn:       conn,
        sessionID:  sessionID,
        state:      sendClosed,
        recvWindow: 65535,
        inflight:   make(map[uint32]*packetRecord),
        bbr:        NewBBR(),
        outbox:     make(chan []byte, 100),
        ackCh:      make(chan ackEvent, 100),
        done:       make(chan struct{}),
        errCh:      make(chan error, 1),
    }
    s.timers = NewTimerManager(500*time.Millisecond, s.onTimeout)
    return s
}
```

### 2.5 sendLoop — Flow Control Guard

```go
// OLD:
for s.inflightBytes() >= s.effectiveWindow() {

// NEW:
for !s.bbr.CanSend(chunkLen, s.recvWindow) {
```

### 2.6 sendLoop — Pacing After Each Chunk

After `s.conn.WriteToUDP(encoded, s.remoteAddr)`, before storing in inflight:

```go
s.bbr.OnSend(chunkLen)

s.inflight[seq] = &packetRecord{
    data:            encoded,
    seq:             seq,
    length:          chunkLen,
    sentAt:          time.Now(),
    deliveredAtSend: s.bbr.GetDelivered(),
}
s.timers.Start(seq)
s.mu.Unlock()

// Non-blocking pacing
if delay := s.bbr.PacingDelay(chunkLen); delay > 0 {
    select {
    case <-time.After(delay):
    case <-s.done:
        return
    case <-s.errCh:
        return
    }
}
```

### 2.7 processAck — Report to BBR

```go
func (s *Sender) processAck(ackNum uint32) {
    if ackNum <= s.sendBase {
        return
    }
    for seq := s.sendBase; seq < ackNum; {
        if rec, ok := s.inflight[seq]; ok {
            rec.done = true
            s.timers.Cancel(seq)
            rtt := time.Since(rec.sentAt)
            s.bbr.OnACK(rec.length, rtt, rec.sentAt, rec.deliveredAtSend)
            delete(s.inflight, seq)
            seq += rec.length
        } else {
            seq++
        }
    }
    s.sendBase = ackNum
}
```

### 2.8 onTimeout — Loss Notification

```go
func (s *Sender) onTimeout(seq uint32) {
    s.mu.Lock()
    defer s.mu.Unlock()

    rec, ok := s.inflight[seq]
    if !ok {
        return
    }

    rec.retransCount++
    if rec.retransCount > maxRetransmits {
        s.bbr.OnLoss(rec.length)     // NEW
        log.Printf("[RDT] max retransmits exceeded for seq=%d", seq)
        s.sendControl(0, 0, FlagRST)
        s.state = sendClosed
        go func() { s.errCh <- fmt.Errorf("max retransmits for seq %d", seq) }()
        close(s.done)
        return
    }

    log.Printf("[RDT] retransmitting seq=%d (attempt %d)", seq, rec.retransCount)
    s.conn.WriteToUDP(rec.data, s.remoteAddr)
}
```

---

## 3. ANSI Progress Bar (`internal/rdt/progress.go`)

### 3.1 Struct & Constants

```go
type ProgressBar struct {
    total       int64
    current     int64
    startTime   time.Time
    lastUpdate  time.Time

    speedWindow []speedSample
    avgSpeed    float64
    spinnerIdx  int

    mu sync.Mutex
}

type speedSample struct {
    bytes int64
    at    time.Time
}

const (
    barWidth        = 30
    speedWindowDur  = 1200 * time.Millisecond
    renderThrottle  = 100 * time.Millisecond
    spinnerRunes    = "|/-\\"
)
```

### 3.2 Public Interface

```go
func NewProgressBar(total int64) *ProgressBar  // total=0 = unknown size
func (p *ProgressBar) Update(n int)            // add n bytes; redraw throttled to 100ms
func (p *ProgressBar) Done()                   // clear line, final stats, trailing \n
```

### 3.3 Update Logic

```go
func (p *ProgressBar) Update(n int) {
    p.mu.Lock()
    defer p.mu.Unlock()

    p.current += int64(n)
    now := time.Now()

    // Always track speed samples (internal state)
    p.speedWindow = append(p.speedWindow, speedSample{int64(n), now})
    p.trimSpeedWindow(now)
    if len(p.speedWindow) > 1 {
        first := p.speedWindow[0]
        dur := p.speedWindow[len(p.speedWindow)-1].at.Sub(first.at)
        if dur > 0 {
            var sum int64
            for _, s := range p.speedWindow {
                sum += s.bytes
            }
            p.avgSpeed = float64(sum) / dur.Seconds()
        }
    }

    // Throttle terminal rendering
    if !now.After(p.lastUpdate.Add(renderThrottle)) {
        return
    }
    p.lastUpdate = now

    if p.total == 0 {
        p.spinnerIdx = (p.spinnerIdx + 1) % 4
    }

    p.render()
}
```

### 3.4 Render

```go
func (p *ProgressBar) render() {
    speed := formatSpeed(p.avgSpeed)

    if p.total > 0 {
        pct := p.current * 100 / p.total
        filled := int(pct) * barWidth / 100
        bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
        overallSpeed := float64(p.current) / time.Since(p.startTime).Seconds()
        var etaStr string
        if overallSpeed > 0 {
            eta := time.Duration(float64(p.total-p.current)/overallSpeed) * time.Second
            etaStr = fmt.Sprintf("ETA: %02d:%02d", int(eta.Minutes()), int(eta.Seconds())%60)
        } else {
            etaStr = "ETA: --:--"
        }
        fmt.Printf("\r\033[K%s %3d%% | %s | %s", bar, pct, speed, etaStr)
    } else {
        spinner := string(spinnerRunes[p.spinnerIdx])
        fmt.Printf("\r\033[K%s %s downloaded | %s", spinner, formatSize(p.current), speed)
    }
}
```

### 3.5 Done

```go
func (p *ProgressBar) Done() {
    p.mu.Lock()
    defer p.mu.Unlock()

    elapsed := time.Since(p.startTime)
    fmt.Printf("\r\033[K")
    if p.total > 0 {
        overall := float64(p.current) / elapsed.Seconds()
        fmt.Printf("Transferred %s in %v (%.1f MiB/s)\n",
            formatSize(p.current), elapsed.Round(time.Millisecond),
            overall/(1<<20))
    } else {
        fmt.Printf("Downloaded %s in %v\n",
            formatSize(p.current), elapsed.Round(time.Millisecond))
    }
}
```

### 3.6 Base-2 Formatting

```go
func formatSpeed(bytesPerSec float64) string {
    switch {
    case bytesPerSec >= 1<<20:
        return fmt.Sprintf("%.1f MiB/s", bytesPerSec/(1<<20))
    case bytesPerSec >= 1<<10:
        return fmt.Sprintf("%.1f KiB/s", bytesPerSec/(1<<10))
    default:
        return fmt.Sprintf("%.0f B/s", bytesPerSec)
    }
}

func formatSize(bytes int64) string {
    switch {
    case bytes >= 1<<20:
        return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
    case bytes >= 1<<10:
        return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
    default:
        return fmt.Sprintf("%d B", bytes)
    }
}
```

---

## 4. PORT Mode (`cmd/client/main.go`)

### 4.1 State Machine

```
After LOGIN (230)
  │
  ▼
modePASV (default) ─────── PORT command ──────► modePORT
  │  Connect() to server                       │  Accept() from server
  │                                            │
  │  transfer done (clear addr)                │  transfer done (close socket, reset)
  ▼                                            ▼
modePASV                                     modePASV
```

- PASV is the default mode after login
- PORT socket is single-use per transfer; client must re-issue PORT for each transfer
- After any transfer, mode resets to PASV

### 4.2 Global State

```go
type dataMode int
const (
    modePASV  dataMode = iota
    modePORT
)

var (
    connDataMode    = modePASV
    portUDPConn     *net.UDPConn
    currentPASVHost string
    currentPASVPort int
)
```

### 4.3 handlePORT

```go
func handlePORT(conn net.Conn) {
    ip4 := conn.LocalAddr().(*net.TCPAddr).IP.To4()
    // NOTE: Returns private LAN IP. Fine for local testing.
    // Real internet would require STUN/UPnP for NAT traversal.

    udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
    if err != nil {
        fmt.Printf("PORT: %v\n", err)
        return
    }

    localPort := udpConn.LocalAddr().(*net.UDPAddr).Port
    p1, p2 := localPort/256, localPort%256
    portCmd := fmt.Sprintf("PORT %d,%d,%d,%d,%d,%d",
        ip4[0], ip4[1], ip4[2], ip4[3], p1, p2)

    io.WriteString(conn, portCmd+"\r\n")
    reply, _ := tcpctrl.ReadLine(conn)
    fmt.Println(reply)

    if strings.HasPrefix(reply, "200") {
        connDataMode = modePORT
        portUDPConn = udpConn
    } else {
        udpConn.Close()
    }
}
```

### 4.4 Transfer Helper

```go
func prepareDataTransfer() (*net.UDPConn, func(), error) {
    switch connDataMode {
    case modePASV:
        addr, err := net.ResolveUDPAddr("udp",
            fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
        if err != nil {
            return nil, nil, fmt.Errorf("PASV address: %w", err)
        }
        udpConn, err := net.ListenUDP("udp", nil)
        if err != nil {
            return nil, nil, fmt.Errorf("ListenUDP: %w", err)
        }
        cleanup := func() {
            udpConn.Close()
            currentPASVHost = ""
            currentPASVPort = 0
        }
        return udpConn, cleanup, nil
    case modePORT:
        if portUDPConn == nil {
            return nil, nil, fmt.Errorf("no PORT socket")
        }
        cleanup := func() {
            portUDPConn.Close()
            portUDPConn = nil
            connDataMode = modePASV
        }
        return portUDPConn, cleanup, nil
    default:
        return nil, nil, fmt.Errorf("no data mode; use PASV or PORT first")
    }
}
```

### 4.5 Updated Transfer Handlers

```go
func handleRETR(conn net.Conn, filename string) {
    // ... send RETR, read 150 as before ...

    udpConn, cleanup, err := prepareDataTransfer()
    if err != nil {
        fmt.Println(err)
        return
    }
    defer cleanup()

    sessionID := uint32(1)
    receiver := rdt.NewReceiver(udpConn, sessionID)

    switch connDataMode {
    case modePASV:
        addr, _ := net.ResolveUDPAddr("udp",
            fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
        if err := receiver.Connect(addr); err != nil {
            fmt.Printf("RDT connect: %v\n", err)
            return
        }
    case modePORT:
        if err := receiver.Accept(); err != nil {
            fmt.Printf("RDT accept: %v\n", err)
            return
        }
    }

    // ... receive loop with progress bar ...
}
```

STOR follows the same pattern with `rdt.NewSender` instead.

**Note:** The switch needs `connDataMode` captured before cleanup closes the socket. For PORT, `cleanup` closes the socket — so the cleanup must be deferred AFTER the transfer. The above pattern captures the mode before the transfer and uses it; the cleanup only runs on function exit.

---

## 5. HASH Command (`cmd/client/main.go`)

### 5.1 Command Enum Addition (`internal/tcpctrl/commands.go`)

```go
// Add to const block:
CmdHASH

// Add to commandNames map:
"hash": CmdHASH
```

### 5.2 computeFileHash

```go
import (
    "crypto/sha256"
    "io"
)

func computeFileHash(filename string) (string, error) {
    f, err := os.Open(filename)
    if err != nil {
        return "", fmt.Errorf("open: %w", err)
    }
    defer f.Close()

    h := sha256.New()
    buf := make([]byte, 64*1024) // 64KB per spec
    if _, err := io.CopyBuffer(h, f, buf); err != nil {
        return "", fmt.Errorf("hash: %w", err)
    }
    return fmt.Sprintf("%x", h.Sum(nil)), nil
}
```

### 5.3 handleHASH

```go
func handleHASH(conn net.Conn, filename string) {
    localHash, err := computeFileHash(filename)
    if err != nil {
        fmt.Printf("HASH: %v\n", err)
        return
    }

    io.WriteString(conn, "HASH "+filename+"\r\n")
    reply, _ := tcpctrl.ReadLine(conn)
    fmt.Println(reply) // Always show reply to user

    if !strings.HasPrefix(reply, "213 ") {
        return // Error code already displayed (e.g., 550 File not found)
    }

    remoteHash := strings.TrimSpace(reply[4:])
    if strings.EqualFold(localHash, remoteHash) {
        fmt.Println("Hashes match — file integrity verified.")
    } else {
        fmt.Printf("MISMATCH!\n  Local:  %s\n  Remote: %s\n", localHash, remoteHash)
    }
}
```

### 5.4 Client Dispatch Addition

```go
case tcpctrl.CmdHASH:
    handleHASH(conn, strings.TrimSpace(line[5:]))
```

---

## 6. File Manifest

| File | Action | Component |
|------|--------|-----------|
| `internal/rdt/bbr.go` | **Create** | BBR congestion control |
| `internal/rdt/bbr_test.go` | **Create** | BBR unit tests |
| `internal/rdt/progress.go` | **Create** | ANSI progress bar |
| `internal/rdt/sender.go` | **Modify** | BBR integration (replace fixedCWND) |
| `internal/tcpctrl/commands.go` | **Modify** | Add CmdHASH |
| `cmd/client/main.go` | **Modify** | PORT/PASV state machine, HASH, progress bar |
| `test/integration/e2e_test.go` | **Modify** | PORT mode test, HASH test |
| `docs/GENAI_LOG.md` | **Modify** | GenAI log entries |
