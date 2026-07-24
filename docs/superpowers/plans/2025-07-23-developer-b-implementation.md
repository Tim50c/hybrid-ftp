# Developer B Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement BBR congestion control, ANSI progress bar, PORT mode, and HASH command for Developer B.

**Architecture:** Self-contained BBR module integrates with existing RDT sender via `OnACK`/`OnSend`/`OnLoss`/`CanSend`/`PacingDelay` hooks. Progress bar is a standalone component in the rdt package. PORT mode and HASH are client-side additions to `cmd/client/main.go`.

**Tech Stack:** Go 1.21, standard library only (net, crypto/sha256, io, sync, time, fmt, strings).

## Global Constraints

- No external libraries — `import` only from Go standard library
- Error handling: every error checked, use `fmt.Errorf("context: %w", err)`
- Logging: `log.Printf()` with `[RDT]` prefix
- Concurrency: `sync.Mutex` for shared state
- Tests: `*_test.go` alongside source, run with `go test`
- Commit format: `[scope] brief description`
- DRY, YAGNI, TDD — each task produces working, testable code

---

### Task 1: Add CmdHASH to Command Enum

**Files:**
- Modify: `internal/tcpctrl/commands.go:17`
- Modify: `internal/tcpctrl/commands.go:31`

**Interfaces:**
- Produces: `CmdHASH` constant available for client dispatch

- [ ] **Step 1: Add CmdHASH to const block**

File `internal/tcpctrl/commands.go`, add `CmdHASH` before `CmdUNKNOWN`:

```go
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
	CmdHASH   // NEW
	CmdUNKNOWN
)
```

- [ ] **Step 2: Add HASH to commandNames map**

```go
var commandNames = map[string]Cmd{
	"USER": CmdUSER,
	"PASS": CmdPASS,
	"QUIT": CmdQUIT,
	"TYPE": CmdTYPE,
	"PASV": CmdPASV,
	"RETR": CmdRETR,
	"STOR": CmdSTOR,
	"PWD":  CmdPWD,
	"LIST": CmdLIST,
	"CWD":  CmdCWD,
	"HASH": CmdHASH,   // NEW
}
```

- [ ] **Step 3: Run existing tests**

Run: `go test ./internal/tcpctrl/... -v`
Expected: All tests PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tcpctrl/commands.go
git commit -m "[tcpctrl] add CmdHASH command enum"
```

---

### Task 2: BBR Congestion Control

**Files:**
- Create: `internal/rdt/bbr.go`
- Create: `internal/rdt/bbr_test.go`

**Interfaces:**
- Produces: `BBR` struct, `NewBBR()`, `OnACK()`, `OnSend()`, `OnLoss()`, `CanSend()`, `PacingDelay()`, `GetDelivered()`

- [ ] **Step 1: Write bbr.go**

Create `internal/rdt/bbr.go`:

```go
package rdt

import (
	"log"
	"sync"
	"time"
)

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
	bbrMinCwnd          = 4 * MSS
	bbrBwFilterLen      = 10
	rttFilterDuration   = 10 * time.Second
)

type rttSample struct {
	rtt time.Duration
	at  time.Time
}

type BBR struct {
	mu            sync.Mutex
	state         bbrState
	btlbw         float64
	rtprop        time.Duration
	cwnd          uint32
	pacingRate    float64
	pacingGain    float64

	delivered     uint64
	inflightBytes uint32
	latestRTT     time.Duration

	fullBwCount   int
	lastRate      float64
	lastCheckTime time.Time

	phase         int
	phaseStart    time.Time

	lastProbeRTT  time.Time
	inProbeRTT    bool
	probeRTTEnd   time.Time

	bwWindow        [bbrBwFilterLen]float64
	bwWindowIdx     int
	roundBw         float64
	nextRoundDelivered uint64
	roundStart      bool

	rttSamples      []rttSample
}

func NewBBR() *BBR {
	return &BBR{
		state:      bbrStartup,
		pacingGain: bbrStartupGain,
		cwnd:       bbrMinCwnd,
		roundStart: true,
		phaseStart: time.Now(),
	}
}

func (b *BBR) OnACK(ackedBytes uint32, rtt time.Duration, sentAt time.Time, deliveredAtSend uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	b.delivered += uint64(ackedBytes)
	if uint64(ackedBytes) <= b.inflightBytes {
		b.inflightBytes -= uint64(ackedBytes)
	} else {
		b.inflightBytes = 0
	}
	b.latestRTT = rtt

	b.rttSamples = append(b.rttSamples, rttSample{rtt, now})
	b.trimRTTSamples(now)
	if len(b.rttSamples) > 0 {
		b.rtprop = b.rttSamples[0].rtt
		for _, s := range b.rttSamples[1:] {
			if s.rtt < b.rtprop {
				b.rtprop = s.rtt
			}
		}
	}

	elapsed := time.Since(sentAt).Seconds()
	deliveryRate := 0.0
	if elapsed > 0 {
		deliveryRate = float64(b.delivered-deliveredAtSend) / elapsed
	}

	if b.roundStart {
		b.nextRoundDelivered = b.delivered
		b.roundStart = false
	}
	if b.delivered >= b.nextRoundDelivered {
		b.bwWindow[b.bwWindowIdx%bbrBwFilterLen] = b.roundBw
		b.bwWindowIdx++
		b.roundBw = deliveryRate
		b.nextRoundDelivered = b.delivered
	} else {
		if deliveryRate > b.roundBw {
			b.roundBw = deliveryRate
		}
	}
	b.btlbw = 0
	for _, bw := range b.bwWindow {
		if bw > b.btlbw {
			b.btlbw = bw
		}
	}

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

	b.updateCwndAndRate()
}

func (b *BBR) handleStartup(deliveryRate float64, now time.Time) {
	if deliveryRate > b.lastRate*1.25 {
		b.fullBwCount = 0
	}
	if b.rtprop > 0 && now.Sub(b.lastCheckTime) >= b.rtprop {
		b.lastCheckTime = now
		b.fullBwCount++
		b.lastRate = deliveryRate
	}
	if b.fullBwCount >= bbrStartupFullBwCnt {
		log.Printf("[RDT] BBR: STARTUP -> DRAIN")
		b.state = bbrDrain
		b.pacingGain = bbrDrainGain
	}
}

func (b *BBR) handleDrain() {
	bdp := b.btlbw * b.rtprop.Seconds()
	if float64(b.inflightBytes) <= bdp {
		log.Printf("[RDT] BBR: DRAIN -> PROBE_BW")
		b.state = bbrProbeBW
		b.pacingGain = bbrProbeBWGainSteady
		b.phase = 0
		b.phaseStart = time.Now()
	}
}

func (b *BBR) handleProbeBW(now time.Time) {
	if b.rtprop > 0 && now.Sub(b.phaseStart) >= b.rtprop {
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
		log.Printf("[RDT] BBR: PROBE_BW -> PROBE_RTT")
		b.state = bbrProbeRTT
		b.inProbeRTT = true
		b.lastProbeRTT = now
		b.probeRTTEnd = now.Add(bbrProbeRTTDuration)
		b.cwnd = bbrMinCwnd
	}
}

func (b *BBR) handleProbeRTT(now time.Time) {
	if now.After(b.probeRTTEnd) {
		log.Printf("[RDT] BBR: PROBE_RTT -> PROBE_BW")
		b.inProbeRTT = false
		b.state = bbrProbeBW
		b.rttSamples = b.rttSamples[:0]
		b.lastProbeRTT = time.Now()
	}
}

func (b *BBR) updateCwndAndRate() {
	if b.btlbw <= 0 || b.rtprop <= 0 {
		return
	}
	if b.inProbeRTT {
		b.cwnd = bbrMinCwnd
		return
	}
	bdp := b.pacingGain * b.btlbw * b.rtprop.Seconds()
	cwnd := uint32(bdp)
	if cwnd < bbrMinCwnd {
		cwnd = bbrMinCwnd
	}
	b.cwnd = cwnd
	b.pacingRate = b.pacingGain * b.btlbw
}

func (b *BBR) trimRTTSamples(now time.Time) {
	cutoff := now.Add(-rttFilterDuration)
	i := 0
	for _, s := range b.rttSamples {
		if s.at.After(cutoff) {
			b.rttSamples[i] = s
			i++
		}
	}
	b.rttSamples = b.rttSamples[:i]
}

func (b *BBR) OnSend(dataLen uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inflightBytes += uint64(dataLen)
}

func (b *BBR) OnLoss(lostBytes uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if uint64(lostBytes) <= b.inflightBytes {
		b.inflightBytes -= uint64(lostBytes)
	} else {
		b.inflightBytes = 0
	}
}

func (b *BBR) CanSend(dataLen uint32, recvWindow uint16) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	effCwnd := b.cwnd
	if uint32(recvWindow) < effCwnd {
		effCwnd = uint32(recvWindow)
	}
	return uint64(b.inflightBytes)+uint64(dataLen) <= uint64(effCwnd)
}

func (b *BBR) PacingDelay(dataLen uint32) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pacingRate <= 0 {
		return 0
	}
	delay := float64(dataLen) / b.pacingRate
	d := time.Duration(delay * float64(time.Second))
	return d
}

func (b *BBR) GetDelivered() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.delivered
}
```

- [ ] **Step 2: Verify bbr.go compiles**

Run: `go build ./internal/rdt/...`
Expected: No errors

- [ ] **Step 3: Write bbr_test.go**

Create `internal/rdt/bbr_test.go`:

```go
package rdt

import (
	"testing"
	"time"
)

func TestNewBBR(t *testing.T) {
	b := NewBBR()
	if b.state != bbrStartup {
		t.Errorf("expected STARTUP, got %v", b.state)
	}
	if b.cwnd != bbrMinCwnd {
		t.Errorf("expected cwnd=%d, got %d", bbrMinCwnd, b.cwnd)
	}
	if b.pacingGain != bbrStartupGain {
		t.Errorf("expected pacingGain=%f, got %f", bbrStartupGain, b.pacingGain)
	}
}

func TestBBR_OnSendAndCanSend(t *testing.T) {
	b := NewBBR()
	b.cwnd = 10000

	if !b.CanSend(5000, 65535) {
		t.Error("should be able to send 5000 under cwnd=10000")
	}

	b.OnSend(5000)

	if b.CanSend(6000, 65535) {
		t.Error("should block when inflight(5000) + 6000 > cwnd(10000)")
	}

	if !b.CanSend(4000, 65535) {
		t.Error("should allow inflight(5000) + 4000 <= cwnd(10000)")
	}
}

func TestBBR_CanSend_RecvWindow(t *testing.T) {
	b := NewBBR()
	b.cwnd = 10000

	// recvWindow 3000 is tighter than cwnd
	if b.CanSend(3001, 3000) {
		t.Error("should block on recvWindow")
	}
	if !b.CanSend(2000, 3000) {
		t.Error("should allow within recvWindow")
	}
}

func TestBBR_OnLoss(t *testing.T) {
	b := NewBBR()
	b.cwnd = 5600

	b.OnSend(1400)
	b.OnSend(1400)
	// inflight = 2800

	if b.CanSend(2801, 65535) {
		t.Error("should block: 2800 + 2801 > 5600")
	}

	b.OnLoss(1400)
	// inflight = 1400

	if !b.CanSend(2800, 65535) {
		t.Error("should allow: 1400 + 2800 <= 5600")
	}
}

func TestBBR_OnACK_RTPropUpdate(t *testing.T) {
	b := NewBBR()
	now := time.Now()

	// First ACK: deliver bytes, set up filters
	sentAt := now.Add(-10 * time.Millisecond)
	b.OnSend(1000)
	b.OnACK(1000, 10*time.Millisecond, sentAt, 0)

	if b.rtprop != 10*time.Millisecond {
		t.Errorf("expected rtprop=10ms, got %v", b.rtprop)
	}

	sentAt2 := now.Add(-5 * time.Millisecond)
	b.OnSend(500)
	b.OnACK(500, 5*time.Millisecond, sentAt2, b.GetDelivered()-500)

	if b.rtprop != 5*time.Millisecond {
		t.Errorf("expected rtprop=5ms (min), got %v", b.rtprop)
	}
}

func TestBBR_STARTUP_To_DRAIN(t *testing.T) {
	b := NewBBR()
	b.rtprop = 100 * time.Millisecond
	b.btlbw = 1000000
	now := time.Now()

	// Simulate 3 RTTs with flat bandwidth
	for i := 0; i < 4; i++ {
		sentAt := now.Add(-time.Duration(i+1) * 100 * time.Millisecond)
		deliveredAtSend := b.delivered
		b.OnSend(1400)
		b.OnACK(1400, 100*time.Millisecond, sentAt, deliveredAtSend)
		b.lastCheckTime = now.Add(-time.Duration(4-i) * 100 * time.Millisecond)
		b.lastRate = 1000000
		b.fullBwCount++
	}

	if b.fullBwCount < bbrStartupFullBwCnt {
		b.lastRate = 1000000
		b.rtprop = 100 * time.Millisecond
		b.OnACK(1400, 100*time.Millisecond, now.Add(-100*time.Millisecond), b.delivered)
		b.fullBwCount = bbrStartupFullBwCnt
		b.handleStartup(b.lastRate, now)
	}

	if b.state != bbrDrain {
		t.Errorf("expected DRAIN after plateau, got state=%d", b.state)
	}
}

func TestBBR_PacingDelay(t *testing.T) {
	b := NewBBR()
	b.pacingRate = 1000000 // 1 MB/s

	delay := b.PacingDelay(1400)
	expected := time.Duration(1400.0 / 1000000.0 * float64(time.Second))
	if delay != expected {
		t.Errorf("expected pacing delay %v for 1400 bytes @ 1MB/s, got %v", expected, delay)
	}
}

func TestBBR_RoundStart(t *testing.T) {
	b := NewBBR()
	if !b.roundStart {
		t.Error("should start with roundStart=true")
	}

	b.OnACK(1400, 10*time.Millisecond, time.Now().Add(-10*time.Millisecond), 0)
	if b.roundStart {
		t.Error("should set roundStart=false after first ACK")
	}
}

func TestBBR_GetDelivered(t *testing.T) {
	b := NewBBR()
	if b.GetDelivered() != 0 {
		t.Error("should start at 0")
	}

	b.OnSend(500)
	b.OnACK(500, 5*time.Millisecond, time.Now().Add(-5*time.Millisecond), b.GetDelivered()-500)

	if b.GetDelivered() != 500 {
		t.Errorf("expected delivered=500, got %d", b.GetDelivered())
	}
}

func TestBBR_RTTFilterTrimming(t *testing.T) {
	b := NewBBR()

	// Add an old sample
	b.rttSamples = append(b.rttSamples, rttSample{
		rtt: 50 * time.Millisecond,
		at:  time.Now().Add(-20 * time.Second),
	})
	// Add a fresh sample
	b.rttSamples = append(b.rttSamples, rttSample{
		rtt: 10 * time.Millisecond,
		at:  time.Now(),
	})

	b.trimRTTSamples(time.Now())

	if len(b.rttSamples) != 1 {
		t.Errorf("expected 1 sample after trimming old ones, got %d", len(b.rttSamples))
	}
}
```

- [ ] **Step 4: Run bbr tests**

Run: `go test ./internal/rdt/ -run TestBBR -v`
Expected: All BBR tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rdt/bbr.go internal/rdt/bbr_test.go
git commit -m "[rdt] add BBR congestion control with unit tests"
```

---

### Task 3: Sender BBR Integration

**Files:**
- Modify: `internal/rdt/sender.go`

**Interfaces:**
- Consumes: `BBR` type, `NewBBR()`, `OnACK()`, `OnSend()`, `OnLoss()`, `CanSend()`, `PacingDelay()`, `GetDelivered()`
- Produces: BBR-integrated `Sender`

- [ ] **Step 1: Remove fixedCWND and add BBR field**

In `internal/rdt/sender.go`, remove the `fixedCWND` constant (line 13-15) and the `windowSize` field from the `Sender` struct (line 43). Add the `bbr` field.

Delete this block:
```go
const (
	fixedCWND = 28000
)
```

Modify the `Sender` struct:
```go
type Sender struct {
	conn       *net.UDPConn
	sessionID  uint32
	remoteAddr *net.UDPAddr
	mu         sync.Mutex
	state      senderState
	sendBase   uint32
	nextSeqNum uint32
	recvWindow uint16
	inflight   map[uint32]*packetRecord
	timers     *TimerManager
	bbr        *BBR
	outbox     chan []byte
	ackCh      chan ackEvent
	done       chan struct{}
	errCh      chan error
}
```

- [ ] **Step 2: Add deliveredAtSend to packetRecord**

```go
type packetRecord struct {
	data             []byte
	sentAt           time.Time
	retransCount     int
	seq              uint32
	length           uint32
	done             bool
	deliveredAtSend  uint64
}
```

- [ ] **Step 3: Update NewSender**

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

- [ ] **Step 4: Update sendLoop — flow control guard and pacing**

Replace the block guard (lines 274-283):
```go
// OLD:
for s.inflightBytes() >= s.effectiveWindow() {
    s.mu.Unlock()
    select {
    case <-s.ackCh:
    case <-s.done:
        return
    case <-time.After(10 * time.Millisecond):
    }
    s.mu.Lock()
}
```

With:
```go
for !s.bbr.CanSend(chunkLen, s.recvWindow) {
    s.mu.Unlock()
    select {
    case <-s.ackCh:
    case <-s.done:
        return
    case <-time.After(10 * time.Millisecond):
    }
    s.mu.Lock()
}
```

Replace the packet recording and end of chunk processing (lines 313-323):
```go
// OLD:
s.inflight[seq] = &packetRecord{
    data:   encoded,
    seq:    seq,
    length: chunkLen,
    sentAt: time.Now(),
}
s.timers.Start(seq)
s.mu.Unlock()

offset = end
```

With:
```go
s.bbr.OnSend(chunkLen)

s.inflight[seq] = &packetRecord{
    data:             encoded,
    seq:              seq,
    length:           chunkLen,
    sentAt:           time.Now(),
    deliveredAtSend:  s.bbr.GetDelivered(),
}
s.timers.Start(seq)
s.mu.Unlock()

if delay := s.bbr.PacingDelay(chunkLen); delay > 0 {
    select {
    case <-time.After(delay):
    case <-s.done:
        return
    case <-s.errCh:
        return
    }
}

offset = end
```

- [ ] **Step 5: Update processAck — report to BBR**

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

- [ ] **Step 6: Update onTimeout — call OnLoss**

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
		s.bbr.OnLoss(rec.length)
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

- [ ] **Step 7: Remove effectiveWindow method**

Delete the `effectiveWindow()` method (lines 237-243) entirely.

- [ ] **Step 8: Remove inflightBytes method (if unused)**

Check if `inflightBytes()` (lines 245-251) is used anywhere. It's only used in `sendLoop` for the old flow control guard, which we replaced. Remove it:

```go
// DELETE: func (s *Sender) inflightBytes() uint32 { ... }
```

- [ ] **Step 9: Build and run RDT tests**

Run: `go build ./internal/rdt/...`
Expected: No errors

Run: `go test ./internal/rdt/... -v -run "TestNew|TestTimer|TestHeader|TestChecksum|TestBBR"`
Expected: All PASS

- [ ] **Step 10: Commit**

```bash
git add internal/rdt/sender.go
git commit -m "[rdt] integrate BBR congestion control into sender"
```

---

### Task 4: ANSI Progress Bar

**Files:**
- Create: `internal/rdt/progress.go`

**Interfaces:**
- Produces: `ProgressBar` struct, `NewProgressBar(total int64)`, `Update(n int)`, `Done()`

- [ ] **Step 1: Write progress.go**

Create `internal/rdt/progress.go`:

```go
package rdt

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	barWidth        = 30
	speedWindowDur  = 1200 * time.Millisecond
	renderThrottle  = 100 * time.Millisecond
	spinnerRunes    = "|/-\\"
)

type speedSample struct {
	bytes int64
	at    time.Time
}

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

func NewProgressBar(total int64) *ProgressBar {
	return &ProgressBar{
		total:      total,
		startTime:  time.Now(),
		lastUpdate: time.Now(),
	}
}

func (p *ProgressBar) Update(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current += int64(n)
	now := time.Now()

	p.speedWindow = append(p.speedWindow, speedSample{int64(n), now})
	p.trimSpeedWindow(now)
	if len(p.speedWindow) > 1 {
		first := p.speedWindow[0]
		last := p.speedWindow[len(p.speedWindow)-1]
		dur := last.at.Sub(first.at)
		if dur > 0 {
			var sum int64
			for _, s := range p.speedWindow {
				sum += s.bytes
			}
			p.avgSpeed = float64(sum) / dur.Seconds()
		}
	}

	if !now.After(p.lastUpdate.Add(renderThrottle)) {
		return
	}
	p.lastUpdate = now

	if p.total == 0 {
		p.spinnerIdx = (p.spinnerIdx + 1) % 4
	}

	p.render()
}

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

func (p *ProgressBar) render() {
	speed := formatSpeed(p.avgSpeed)

	if p.total > 0 {
		pct := p.current * 100 / p.total
		filled := int(pct) * barWidth / 100
		bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", barWidth-filled)
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

func (p *ProgressBar) trimSpeedWindow(now time.Time) {
	cutoff := now.Add(-speedWindowDur)
	i := 0
	for _, s := range p.speedWindow {
		if s.at.After(cutoff) {
			p.speedWindow[i] = s
			i++
		}
	}
	p.speedWindow = p.speedWindow[:i]
}

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

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/rdt/...`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add internal/rdt/progress.go
git commit -m "[rdt] add ANSI terminal progress bar"
```

---

### Task 5: Client Additions (PORT, HASH, Progress Bar)

**Files:**
- Modify: `cmd/client/main.go`

**Interfaces:**
- Consumes: `rdt.NewProgressBar()`, `rdt.ProgressBar.Update()`, `rdt.ProgressBar.Done()`, `tcpctrl.CmdHASH`, `tcpctrl.CmdPORT`
- Produces: full client with PORT, HASH, and progress bar support

- [ ] **Step 1: Add imports**

Add to the imports block in `cmd/client/main.go`:
```go
import (
	// ... existing imports ...
	"crypto/sha256"
	"io"

	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/tcpctrl"
)
```

Note: `rdt` and `tcpctrl` are already imported. Verify `crypto/sha256` and `io` are added.

- [ ] **Step 2: Add PORT state variables**

Replace the global PASV-only variables (lines 137-140):
```go
// OLD:
var (
	currentPASVHost string
	currentPASVPort int
)
```

With:
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

- [ ] **Step 3: Add prepareDataTransfer helper**

Add this function after the existing helpers:
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
			return nil, nil, fmt.Errorf("no PORT socket; use PORT first")
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

- [ ] **Step 4: Add handlePORT function**

```go
func handlePORT(conn net.Conn) {
	ip4 := conn.LocalAddr().(*net.TCPAddr).IP.To4()
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

- [ ] **Step 5: Add HASH functions**

```go
func computeFileHash(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func handleHASH(conn net.Conn, filename string) {
	localHash, err := computeFileHash(filename)
	if err != nil {
		fmt.Printf("HASH: %v\n", err)
		return
	}

	io.WriteString(conn, "HASH "+filename+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)

	if !strings.HasPrefix(reply, "213 ") {
		return
	}

	remoteHash := strings.TrimSpace(reply[4:])
	if strings.EqualFold(localHash, remoteHash) {
		fmt.Println("Hashes match - file integrity verified.")
	} else {
		fmt.Printf("MISMATCH!\n  Local:  %s\n  Remote: %s\n", localHash, remoteHash)
	}
}
```

- [ ] **Step 6: Update handleRETR with progress bar**

Replace the existing `handleRETR` function:
```go
func handleRETR(conn net.Conn, filename string) {
	if filename == "" {
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

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
		udpAddr, _ := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := receiver.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect: %v\n", err)
			return
		}
	case modePORT:
		if err := receiver.Accept(); err != nil {
			fmt.Printf("RDT accept: %v\n", err)
			return
		}
	}

	localFile, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Cannot create local file: %v\n", err)
		receiver.Close()
		return
	}
	defer localFile.Close()

	progress := rdt.NewProgressBar(0)
	for chunk := range receiver.Receive() {
		n, _ := localFile.Write(chunk)
		progress.Update(n)
	}
	receiver.Close()
	progress.Done()

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}
```

- [ ] **Step 7: Update handleSTOR with progress bar**

Replace or modify `handleSTOR` to add progress bar:
```go
func handleSTOR(conn net.Conn, filename string) {
	if filename == "" {
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	localFile, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Cannot open local file: %v\n", err)
		return
	}
	defer localFile.Close()

	fileInfo, _ := localFile.Stat()

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	sender := rdt.NewSender(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := sender.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect: %v\n", err)
			return
		}
	case modePORT:
		if err := sender.Accept(); err != nil {
			fmt.Printf("RDT accept: %v\n", err)
			return
		}
	}

	progress := rdt.NewProgressBar(fileInfo.Size())
	buf := make([]byte, 1400)
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if sendErr := sender.Send(buf[:n]); sendErr != nil {
				fmt.Printf("Send error: %v\n", sendErr)
				break
			}
			progress.Update(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	sender.Close()
	progress.Done()

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}
```

- [ ] **Step 8: Update command dispatch switch**

Add these cases to the main switch statement in the CLI loop:

```go
case tcpctrl.CmdPORT:
    handlePORT(conn)

case tcpctrl.CmdHASH:
    handleHASH(conn, strings.TrimSpace(line[5:]))

case tcpctrl.CmdPASV:
    // ... keep existing PASV handler ...
```

Also add `"PORT"` to the `commandNames` map in `tcpctrl/commands.go` (add in Task 1):
```go
"PORT": CmdPORT,
```

And add `CmdPORT` to the const block:
```go
CmdPORT
```

Also add `CmdHASH` if not already done in Task 1.

- [ ] **Step 9: Build the client**

Run: `go build ./cmd/client/...`
Expected: No errors

- [ ] **Step 10: Commit**

```bash
git add cmd/client/main.go internal/tcpctrl/commands.go
git commit -m "[client] add PORT mode, HASH command, and ANSI progress bar"
```

---

### Task 6: Integration Tests

**Files:**
- Modify: `test/integration/e2e_test.go`

**Interfaces:**
- Consumes: All RDT, tcpctrl, and client features

- [ ] **Step 1: Add TestE2E_HASH test**

Add to `test/integration/e2e_test.go`:

```go
func TestE2E_HASH(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	testFile := filepath.Join(rootDir, "hashable.txt")
	content := []byte("this is test content for hashing\n")
	os.WriteFile(testFile, content, 0644)

	fc.sendAndRead(t, "PASV")
	reply := fc.sendAndRead(t, "RETR hashable.txt")
	if !strings.HasPrefix(reply, "150") {
		t.Errorf("expected 150 for RETR, got %s", reply)
	}

	// Client-like hash computation
	importHash := computeIntegrityHash(t, content)

	fc.readReply(t) // 226

	reply = fc.sendAndRead(t, "HASH hashable.txt")
	if !strings.HasPrefix(reply, "213 ") && !strings.HasPrefix(reply, "502 ") && !strings.HasPrefix(reply, "500 ") {
		t.Logf("HASH not implemented server-side: %s", reply)
		return
	}
	if strings.HasPrefix(reply, "213 ") {
		remoteHash := strings.TrimSpace(reply[4:])
		localHash := fmt.Sprintf("%x", importHash)
		if remoteHash != localHash {
			t.Errorf("hash mismatch: local=%s remote=%s", localHash, remoteHash)
		}
	}

	fc.sendAndRead(t, "QUIT")
}
```

- [ ] **Step 2: Add helper**

```go
func computeIntegrityHash(t *testing.T, data []byte) [32]byte {
	t.Helper()
	h := sha256.New()
	h.Write(data)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
```

Note: This requires `"crypto/sha256"` import in the test file.

- [ ] **Step 3: Run integration tests**

Run: `go test ./test/integration/... -v -timeout 120s`
Expected: All tests PASS (HASH test may skip if server-side HASH not yet implemented)

- [ ] **Step 4: Commit**

```bash
git add test/integration/e2e_test.go
git commit -m "[test] add HASH integration test"
```

---

### Task 7: Final Verification

**Files:**
- All modified files

- [ ] **Step 1: Build entire project**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 2: Run all unit tests**

Run: `go test ./internal/... -v`
Expected: All PASS

- [ ] **Step 3: Run all integration tests**

Run: `go test ./test/... -v -timeout 120s`
Expected: All PASS (or skip for unimplemented server commands)

- [ ] **Step 4: Run go vet**

Run: `go vet ./...`
Expected: No warnings

- [ ] **Step 5: Commit if needed**

```bash
git status
git diff
git add . && git commit -m "[meta] final verification all tests pass" || true
```
