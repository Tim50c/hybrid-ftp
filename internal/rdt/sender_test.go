package rdt

import (
	"testing"
	"time"
)

func TestProcessAckSkipsGaps(t *testing.T) {
	s := &Sender{
		inflight:   make(map[uint32]*packetRecord),
		sendBase:   0,
		recvWindow: 65535,
		bbr:        NewBBR(),
	}
	s.timers = NewTimerManager(time.Second, func(seq uint32) {})

	for i := uint32(0); i < 7000; i += 1400 {
		s.inflight[i] = &packetRecord{
			seq:    i,
			length: 1400,
			sentAt: time.Now(),
		}
	}

	s.processAck(7000)

	if s.sendBase != 7000 {
		t.Errorf("sendBase = %d, want 7000", s.sendBase)
	}
	if len(s.inflight) != 0 {
		t.Errorf("inflight not empty after ack: %d entries remain", len(s.inflight))
	}
}

func TestProcessAckPartialAck(t *testing.T) {
	s := &Sender{
		inflight:   make(map[uint32]*packetRecord),
		sendBase:   0,
		recvWindow: 65535,
		bbr:        NewBBR(),
	}
	s.timers = NewTimerManager(time.Second, func(seq uint32) {})

	for i := uint32(0); i < 7000; i += 1400 {
		s.inflight[i] = &packetRecord{
			seq:    i,
			length: 1400,
			sentAt: time.Now(),
		}
	}

	s.processAck(2800)

	if s.sendBase != 2800 {
		t.Errorf("sendBase = %d, want 2800", s.sendBase)
	}
	if len(s.inflight) != 3 {
		t.Errorf("inflight has %d entries after partial ack, want 3", len(s.inflight))
	}
}
