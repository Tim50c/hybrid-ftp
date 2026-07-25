package rdt

import (
	"net"
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

func TestSenderCloseDrainsOutbox(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:60000")
	if err != nil {
		t.Fatal(err)
	}

	s := &Sender{
		conn:       conn,
		remoteAddr: addr,
		outbox:     make(chan []byte, 10),
		inflight:   make(map[uint32]*packetRecord),
		done:       make(chan struct{}),
		state:      sendEstablished,
		bbr:        NewBBR(),
		recvWindow: 65535,
	}
	s.timers = NewTimerManager(time.Second, func(seq uint32) {})

	for i := 0; i < 3; i++ {
		s.outbox <- []byte("data-chunk")
	}

	s.mu.Lock()
	s.inflight[1000] = &packetRecord{
		seq:    1000,
		length: 1400,
		sentAt: time.Now(),
	}
	s.mu.Unlock()

	go func() {
		time.Sleep(50 * time.Millisecond)
		for i := 0; i < 3; i++ {
			select {
			case <-s.outbox:
			default:
			}
		}
	}()

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.mu.Lock()
		delete(s.inflight, 1000)
		s.mu.Unlock()
		close(s.done)
	}()

	start := time.Now()
	err = s.Close()
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("Close returned too quickly: %v, want >= 100ms (drain not exercised)", elapsed)
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
