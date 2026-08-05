package rdt

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type senderState int

const (
	sendClosed senderState = iota
	sendEstablished
	sendFinSent
)

type packetRecord struct {
	data             []byte
	sentAt           time.Time
	retransCount     int
	seq              uint32
	length           uint32
	done             bool
	deliveredAtSend  uint64
}

type Sender struct {
	conn       UDPConn
	remoteAddr *net.UDPAddr
	mu         sync.Mutex
	state      senderState
	sendBase   uint32
	nextSeqNum uint32
	recvWindow uint32
	peerScale  uint8
	inflight   map[uint32]*packetRecord
	timers     *TimerManager
	bbr        *BBR
	outbox     chan []byte
	done       chan struct{}
	errCh      chan error
	closeOnce  sync.Once
	activeSend bool
	recvReady  chan struct{}
}

func NewSender(conn UDPConn) *Sender {
	s := &Sender{
		conn:       conn,
		state:      sendClosed,
		recvWindow: 65535,
		inflight:   make(map[uint32]*packetRecord),
		bbr:        NewBBR(),
		outbox:     make(chan []byte, 100),
		done:       make(chan struct{}),
		errCh:      make(chan error, 1),
		recvReady:  make(chan struct{}),
	}
	s.timers = NewTimerManager(500*time.Millisecond, s.onTimeout)
	return s
}

func (s *Sender) Start(addr *net.UDPAddr) error {
	s.mu.Lock()
	s.remoteAddr = addr
	s.state = sendEstablished
	s.mu.Unlock()

	h := &Header{
		AckNum: 0,
		Flags:  FlagACK,
		Window: 65535,
	}
	encoded, _ := h.Encode(nil)
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)
	s.conn.WriteToUDP(encoded, s.remoteAddr)

	go s.sendLoop()
	go s.recvLoop()
	return nil
}

func (s *Sender) Accept() error {
	s.mu.Lock()
	s.state = sendEstablished
	recvReady := s.recvReady
	s.mu.Unlock()

	go s.sendLoop()
	go s.recvLoop()

	select {
	case <-recvReady:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("accept timed out waiting for peer")
	}
}

func (s *Sender) Send(data []byte) error {
	copied := make([]byte, len(data))
	copy(copied, data)
	select {
	case s.outbox <- copied:
		return nil
	case <-s.done:
		return fmt.Errorf("sender closed")
	case <-s.errCh:
		err := <-s.errCh
		return err
	}
}

func (s *Sender) Close() error {
	s.mu.Lock()
	if s.state != sendEstablished {
		s.mu.Unlock()
		return fmt.Errorf("sender not established")
	}
	s.state = sendFinSent
	s.mu.Unlock()

	emptyCount := 0
	for emptyCount < 3 {
		select {
		case <-s.done:
			return nil
		default:
		}
		s.mu.Lock()
		outboxEmpty := len(s.outbox) == 0 && !s.activeSend
		inflightEmpty := len(s.inflight) == 0
		s.mu.Unlock()
		if outboxEmpty && inflightEmpty {
			emptyCount++
		} else {
			emptyCount = 0
		}
		time.Sleep(100 * time.Millisecond)
	}

	select {
	case <-s.done:
		return nil
	case <-time.After(100 * time.Millisecond):
	}

	if err := s.sendControl(FlagFIN); err != nil {
		return fmt.Errorf("close fin: %w", err)
	}

	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func (s *Sender) sendControl(flags uint8) error {
	h := &Header{
		SeqNum:     0,
		AckNum:     0,
		Flags:      flags,
		PayloadLen: 0,
		Window:     65535,
	}
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode control: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)
	_, err = s.conn.WriteToUDP(encoded, s.remoteAddr)
	if err != nil {
		return fmt.Errorf("send control: %w", err)
	}
	return nil
}

func (s *Sender) sendLoop() {
	log.Printf("[RDT] sender loop started")
	for {
		select {
		case <-s.done:
			return
		default:
		}

		var data []byte
		select {
		case data = <-s.outbox:
		case <-s.done:
			return
		case <-s.errCh:
			return
		}

		s.mu.Lock()
		s.activeSend = true
		s.mu.Unlock()

		offset := 0
		for offset < len(data) {
			end := offset + 1400
			if end > len(data) {
				end = len(data)
			}
			chunkLen := uint32(end - offset)

			s.mu.Lock()
			for !s.bbr.CanSend(chunkLen, s.recvWindow) {
				s.mu.Unlock()
				select {
				case <-s.done:
					return
				case <-time.After(10 * time.Millisecond):
				}
				s.mu.Lock()
			}

			chunk := data[offset:end]

			seq := s.nextSeqNum
			s.nextSeqNum += chunkLen

			h := &Header{
				SeqNum:     seq,
				AckNum:     0,
				Flags:      FlagDATA,
				PayloadLen: uint16(chunkLen),
				Window:     65535,
			}
			encoded, _ := h.Encode(chunk)
			checksum := ComputeChecksum(encoded)
			binary.BigEndian.PutUint32(encoded[13:17], checksum)

			if _, err := s.conn.WriteToUDP(encoded, s.remoteAddr); err != nil {
				log.Printf("[RDT] sender write error: %v", err)
				s.mu.Unlock()
				return
			}

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
		}

		s.mu.Lock()
		s.activeSend = false
		s.mu.Unlock()
	}
}

func (s *Sender) recvLoop() {
	log.Printf("[RDT] sender recv loop started")
	for {
		select {
		case <-s.done:
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 1500)
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("[RDT] sender recv error: %v", err)
			return
		}

		if !VerifyChecksum(buf[:n]) {
			continue
		}

		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}

		s.mu.Lock()

		if s.remoteAddr == nil {
			s.remoteAddr = addr
			if s.recvReady != nil {
				close(s.recvReady)
				s.recvReady = nil
			}
		}

		switch {
		case hdr.Flags&(FlagFIN|FlagACK) == FlagFIN|FlagACK:
			s.state = sendClosed
			s.mu.Unlock()
			s.closeOnce.Do(func() { close(s.done) })
			return

		case hdr.Flags&FlagACK != 0:
			s.recvWindow = hdr.EffectiveWindow()
			s.processAck(hdr.AckNum)
			s.mu.Unlock()

		default:
			s.mu.Unlock()
		}
	}
}

func (s *Sender) processAck(ackNum uint32) {
	if ackNum <= s.sendBase {
		return
	}

	seq := s.sendBase
	for seq < ackNum {
		rec, ok := s.inflight[seq]
		if ok {
			rec.done = true
			s.timers.Cancel(seq)
			rtt := time.Since(rec.sentAt)
			s.bbr.OnACK(rec.length, rtt, rec.sentAt, rec.deliveredAtSend)
			delete(s.inflight, seq)
			seq += rec.length
		} else {
			nextSeq := ackNum
			for sKey := range s.inflight {
				if sKey > seq && sKey < nextSeq {
					nextSeq = sKey
				}
			}
			seq = nextSeq
		}
	}

	s.sendBase = ackNum
}

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
		s.sendControl(FlagFIN)
		s.state = sendClosed
		go func() { s.errCh <- fmt.Errorf("max retransmits for seq %d", seq) }()
		s.closeOnce.Do(func() { close(s.done) })
		return
	}

	log.Printf("[RDT] retransmitting seq=%d (attempt %d)", seq, rec.retransCount)
	s.conn.WriteToUDP(rec.data, s.remoteAddr)
	s.timers.Start(seq)
}
