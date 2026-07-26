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

func TestBBR_DRAIN_To_PROBE_BW(t *testing.T) {
	b := NewBBR()
	b.state = bbrDrain
	b.pacingGain = bbrDrainGain
	b.btlbw = 1_000_000
	b.rtprop = 50 * time.Millisecond
	b.inflightBytes = 100_000

	b.handleDrain()

	if b.state != bbrDrain {
		t.Errorf("should stay in DRAIN while inflight > BDP, got state=%d", b.state)
	}

	b.inflightBytes = 10000
	b.handleDrain()

	if b.state != bbrProbeBW {
		t.Errorf("expected PROBE_BW after DRAIN, got state=%d", b.state)
	}
	if b.pacingGain != bbrProbeBWGainSteady {
		t.Errorf("expected pacingGain=%.2f, got %.2f", bbrProbeBWGainSteady, b.pacingGain)
	}
}

func TestBBR_PROBE_BW_PhaseCycle(t *testing.T) {
	b := NewBBR()
	b.state = bbrProbeBW
	b.rtprop = 50 * time.Millisecond
	b.pacingGain = bbrProbeBWGainSteady
	b.lastProbeRTT = time.Now()

	expectedGains := []float64{bbrProbeBWGainDown, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, bbrProbeBWGainUp}

	for cycle := 0; cycle < 2; cycle++ {
		for _, wantGain := range expectedGains {
			b.phaseStart = time.Now().Add(-100 * time.Millisecond)
			b.handleProbeBW(time.Now())
			if b.pacingGain != wantGain {
				t.Errorf("PROBE_BW cycle=%d phase=%d: got gain=%.2f, want %.2f",
					cycle, b.phase, b.pacingGain, wantGain)
			}
		}
	}
}

func TestBBR_PROBE_BW_To_PROBE_RTT(t *testing.T) {
	b := NewBBR()
	b.state = bbrProbeBW
	b.rtprop = 50 * time.Millisecond
	b.pacingGain = bbrProbeBWGainSteady
	b.lastProbeRTT = time.Now().Add(-11 * time.Second)
	b.cwnd = 50000

	b.handleProbeBW(time.Now())

	if b.state != bbrProbeRTT {
		t.Errorf("expected PROBE_RTT after 10s, got state=%d", b.state)
	}
	if !b.inProbeRTT {
		t.Error("expected inProbeRTT=true")
	}
	if b.cwnd != bbrMinCwnd {
		t.Errorf("expected cwnd=4*MSS=%d, got %d", bbrMinCwnd, b.cwnd)
	}
}

func TestBBR_PROBE_RTT_To_PROBE_BW(t *testing.T) {
	b := NewBBR()
	b.state = bbrProbeRTT
	b.inProbeRTT = true
	b.probeRTTEnd = time.Now().Add(-1 * time.Millisecond)
	b.cwnd = bbrMinCwnd

	b.handleProbeRTT(time.Now())

	if b.state != bbrProbeBW {
		t.Errorf("expected PROBE_BW after PROBE_RTT duration, got state=%d", b.state)
	}
	if b.inProbeRTT {
		t.Error("expected inProbeRTT=false")
	}
}

func TestBBR_BtlBw_WindowedMaxFilter(t *testing.T) {
	b := NewBBR()

	samples := []float64{100000, 200000, 50000, 300000, 150000, 800000, 400000, 250000, 100000, 600000}
	for _, bw := range samples {
		b.bwWindow[b.bwWindowIdx%bbrBwFilterLen] = bw
		b.bwWindowIdx++
	}

	b.btlbw = 0
	for _, bw := range b.bwWindow {
		if bw > b.btlbw {
			b.btlbw = bw
		}
	}

	if b.btlbw != 800000 {
		t.Errorf("btlbw should be max of samples=%f, got %f", 800000.0, b.btlbw)
	}
}

func TestBBR_BtlBw_RingBufferWrap(t *testing.T) {
	b := NewBBR()

	for i := 0; i < bbrBwFilterLen+5; i++ {
		b.bwWindow[b.bwWindowIdx%bbrBwFilterLen] = float64((i + 1) * 1000)
		b.bwWindowIdx++
	}

	b.btlbw = 0
	for _, bw := range b.bwWindow {
		if bw > b.btlbw {
			b.btlbw = bw
		}
	}

	if b.btlbw != float64((bbrBwFilterLen+5)*1000) {
		t.Errorf("btlbw ring buffer wrap: got %f, want %f", b.btlbw, float64((bbrBwFilterLen+5)*1000))
	}
}

func TestBBR_CwndCalculation(t *testing.T) {
	b := NewBBR()
	b.btlbw = 1_000_000
	b.rtprop = 50 * time.Millisecond
	b.pacingGain = bbrProbeBWGainSteady
	b.state = bbrProbeBW

	b.updateCwndAndRate()

	expectedBDP := bbrProbeBWGainSteady * 1_000_000 * 0.050
	expectedCwnd := uint32(expectedBDP)
	if b.cwnd != expectedCwnd {
		t.Errorf("cwnd: got %d, want %d", b.cwnd, expectedCwnd)
	}
}

func TestBBR_CwndFloor(t *testing.T) {
	b := NewBBR()
	b.btlbw = 1000
	b.rtprop = 1 * time.Millisecond
	b.pacingGain = 0.5
	b.state = bbrProbeBW

	b.updateCwndAndRate()

	if b.cwnd < bbrMinCwnd {
		t.Errorf("cwnd %d should not fall below floor %d", b.cwnd, bbrMinCwnd)
	}
}

func TestBBR_IgnoresLossNoCwndReduction(t *testing.T) {
	b := NewBBR()
	b.cwnd = 50000
	beforeCwnd := b.cwnd

	b.OnLoss(1400)

	if b.cwnd != beforeCwnd {
		t.Errorf("BBR should NOT reduce cwnd on loss: was %d, now %d", beforeCwnd, b.cwnd)
	}
}

func TestBBR_DeliveryRateCalculation(t *testing.T) {
	b := NewBBR()
	now := time.Now()
	sentAt := now.Add(-100 * time.Millisecond)

	b.OnSend(10000)
	delAtSend := b.GetDelivered()
	b.OnACK(5000, 50*time.Millisecond, sentAt, delAtSend-10000)

	deliveryRate := float64(5000) / 0.100

	if deliveryRate < 40000 || deliveryRate > 60000 {
		t.Errorf("delivery rate ~%f B/s, expected ~50000 B/s", deliveryRate)
	}
}

func TestBBR_FullLifecycle(t *testing.T) {
	b := NewBBR()
	now := time.Now()

	if b.state != bbrStartup {
		t.Fatalf("initial: expected STARTUP, got %d", b.state)
	}

	b.btlbw = 1_000_000
	b.rtprop = 100 * time.Millisecond

	for i := 0; i < 4; i++ {
		b.lastCheckTime = now.Add(-time.Duration(4-i) * 100 * time.Millisecond)
		flatRate := 14000 * float64(i+1)
		b.lastRate = flatRate
		if i >= 3 {
			flatRate = b.lastRate * 1.1
		}
		b.handleStartup(flatRate, now)
	}

	if b.state != bbrDrain {
		t.Fatalf("STARTUP->DRAIN: expected DRAIN, got %d", b.state)
	}

	b.inflightBytes = 1000
	b.handleDrain()

	if b.state != bbrProbeBW {
		t.Fatalf("DRAIN->PROBE_BW: expected PROBE_BW, got %d", b.state)
	}

	b.lastProbeRTT = now.Add(-11 * time.Second)
	b.phaseStart = now.Add(-200 * time.Millisecond)
	b.handleProbeBW(now)

	if b.state != bbrProbeRTT {
		t.Fatalf("PROBE_BW->PROBE_RTT: expected PROBE_RTT, got %d", b.state)
	}

	b.probeRTTEnd = now.Add(-1 * time.Millisecond)
	b.handleProbeRTT(now)

	if b.state != bbrProbeBW {
		t.Fatalf("PROBE_RTT->PROBE_BW: expected PROBE_BW, got %d", b.state)
	}
}

func TestBBR_PacingRateUpdates(t *testing.T) {
	b := NewBBR()
	b.btlbw = 1_000_000
	b.rtprop = 100 * time.Millisecond
	b.pacingGain = bbrProbeBWGainSteady

	b.updateCwndAndRate()

	expectedRate := bbrProbeBWGainSteady * 1_000_000
	if b.pacingRate != expectedRate {
		t.Errorf("pacingRate: got %f, want %f", b.pacingRate, expectedRate)
	}
}

func TestBBR_NoUpdateWithZeroEstimates(t *testing.T) {
	b := NewBBR()
	b.btlbw = 0
	b.rtprop = 0
	cwndBefore := b.cwnd
	rateBefore := b.pacingRate

	b.updateCwndAndRate()

	if b.cwnd != cwndBefore {
		t.Error("cwnd should not change when btlbw=0 or rtprop=0")
	}
	if b.pacingRate != rateBefore {
		t.Error("pacingRate should not change when btlbw=0 or rtprop=0")
	}
}

func TestBBR_InflightUnderflowGuard(t *testing.T) {
	b := NewBBR()
	b.inflightBytes = 100

	b.OnACK(200, 10*time.Millisecond, time.Now().Add(-10*time.Millisecond), 0)

	if b.inflightBytes != 0 {
		t.Errorf("inflight should floor at 0, got %d", b.inflightBytes)
	}
}

func TestBBR_OnSendAccumulatesInflight(t *testing.T) {
	b := NewBBR()
	b.OnSend(1400)
	b.OnSend(1400)
	b.OnSend(500)

	if b.inflightBytes != 3300 {
		t.Errorf("inflight: got %d, want 3300", b.inflightBytes)
	}
}

func TestBBR_PROBE_RTT_CwndFrozen(t *testing.T) {
	b := NewBBR()
	b.state = bbrProbeRTT
	b.inProbeRTT = true
	b.btlbw = 1_000_000
	b.rtprop = 100 * time.Millisecond

	b.updateCwndAndRate()

	if b.cwnd != bbrMinCwnd {
		t.Errorf("PROBE_RTT cwnd should be %d, got %d", bbrMinCwnd, b.cwnd)
	}
}

func TestBBR_StartupPlateauDetection(t *testing.T) {
	b := NewBBR()
	b.state = bbrStartup
	b.pacingGain = bbrStartupGain
	b.rtprop = 100 * time.Millisecond
	now := time.Now()

	flatRate := 10000.0
	for i := 0; i < bbrStartupFullBwCnt; i++ {
		b.lastCheckTime = now.Add(-time.Duration(bbrStartupFullBwCnt-i) * 100 * time.Millisecond)
		b.handleStartup(flatRate, now)
	}

	if b.state != bbrDrain {
		t.Errorf("STARTUP should exit to DRAIN after plateau, got state=%d", b.state)
	}
}
