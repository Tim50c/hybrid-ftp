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
	now := time.Now()

	for i := 0; i < 4; i++ {
		b.lastCheckTime = now.Add(-time.Duration(4-i) * 100 * time.Millisecond)
		b.lastRate = 14000 * float64(i+1)
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

	deliveredAtSend := b.GetDelivered()
	b.OnSend(500)
	b.OnACK(500, 5*time.Millisecond, time.Now().Add(-5*time.Millisecond), deliveredAtSend)

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
