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
	inflightBytes uint64
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
		state:        bbrStartup,
		pacingGain:   bbrStartupGain,
		cwnd:         bbrMinCwnd,
		roundStart:   true,
		phaseStart:   time.Now(),
		lastProbeRTT: time.Now(),
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
	if b.delivered >= deliveredAtSend && elapsed > 0 {
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

func (b *BBR) CanSend(dataLen uint32, recvWindow uint32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	effCwnd := b.cwnd
	if recvWindow < effCwnd {
		effCwnd = recvWindow
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
