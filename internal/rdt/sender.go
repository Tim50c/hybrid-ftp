package rdt

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	fixedCWND = 28000
)

type senderState int

const (
	sendClosed senderState = iota
	sendSynSent
	sendEstablished
	sendFinSent
)

type packetRecord struct {
	data          []byte
	sentAt        time.Time
	retransCount  int
	seq           uint32
	length        uint32
	done          bool
}

type Sender struct {
	conn       *net.UDPConn
	sessionID  uint32
	remoteAddr *net.UDPAddr
	mu         sync.Mutex
	state      senderState
	sendBase   uint32
	nextSeqNum uint32
	windowSize uint32
	recvWindow uint16
	inflight   map[uint32]*packetRecord
	timers     *TimerManager
	outbox     chan []byte
	ackCh      chan ackEvent
	done       chan struct{}
	errCh      chan error
}

type ackEvent struct {
	ackNum uint32
	window uint16
}

func NewSender(conn *net.UDPConn, sessionID uint32) *Sender {
	s := &Sender{
		conn:       conn,
		sessionID:  sessionID,
		state:      sendClosed,
		windowSize: fixedCWND,
		recvWindow: 65535,
		inflight:   make(map[uint32]*packetRecord),
		outbox:     make(chan []byte, 100),
		ackCh:      make(chan ackEvent, 100),
		done:       make(chan struct{}),
		errCh:      make(chan error, 1),
	}
	s.timers = NewTimerManager(500*time.Millisecond, s.onTimeout)
	return s
}

func (s *Sender) Connect(addr *net.UDPAddr) error {
	s.mu.Lock()
	s.remoteAddr = addr
	s.mu.Unlock()

	myISN := uint32(rand.Int31())
	if err := s.sendControl(myISN, 0, FlagSYN); err != nil {
		return fmt.Errorf("connect syn: %w", err)
	}

	s.mu.Lock()
	s.state = sendSynSent
	s.mu.Unlock()

	for {
		buf := make([]byte, 1500)
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("connect read: %w", err)
		}
		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}
		if hdr.SessionID != s.sessionID || hdr.Flags != (FlagSYN|FlagACK) {
			continue
		}

		s.mu.Lock()
		s.recvWindow = hdr.Window
		s.sendBase = myISN + 1
		s.nextSeqNum = myISN + 1
		if err := s.sendControl(0, hdr.SeqNum+1, FlagACK); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("connect ack: %w", err)
		}
		s.state = sendEstablished
		s.mu.Unlock()

		go s.sendLoop()
		go s.recvLoop()
		return nil
	}
}

func (s *Sender) Accept() error {
	s.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		buf := make([]byte, 1500)
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("accept read: %w", err)
		}

		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}
		if hdr.SessionID != s.sessionID || hdr.Flags != FlagSYN {
			continue
		}

		s.mu.Lock()
		s.remoteAddr = addr
		s.recvWindow = hdr.Window
		myISN := uint32(rand.Int31())
		s.sendBase = myISN + 1
		s.nextSeqNum = myISN + 1
		s.sendControl(myISN, hdr.SeqNum+1, FlagSYN|FlagACK)
		s.mu.Unlock()

		for {
			buf := make([]byte, 1500)
			n, _, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				return fmt.Errorf("accept third ack: %w", err)
			}
			hdr, _, err := Decode(buf[:n])
			if err != nil {
				continue
			}
			if !VerifyChecksum(buf[:n]) {
				continue
			}
			if hdr.SessionID != s.sessionID || hdr.Flags != FlagACK {
				continue
			}
			break
		}

		s.mu.Lock()
		s.state = sendEstablished
		s.mu.Unlock()

		go s.sendLoop()
		go s.recvLoop()
		return nil
	}
}

func (s *Sender) Send(data []byte) error {
	select {
	case s.outbox <- data:
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

	time.Sleep(100 * time.Millisecond)

	if err := s.sendControl(0, 0, FlagFIN); err != nil {
		return fmt.Errorf("close fin: %w", err)
	}

	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func (s *Sender) sendControl(seqNum, ackNum uint32, flags uint8) error {
	h := &Header{
		SessionID:  s.sessionID,
		SeqNum:     seqNum,
		AckNum:     ackNum,
		Flags:      flags,
		PayloadLen: 0,
		Window:     65535,
	}
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode control: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)
	_, err = s.conn.WriteToUDP(encoded, s.remoteAddr)
	if err != nil {
		return fmt.Errorf("send control: %w", err)
	}
	return nil
}

func (s *Sender) effectiveWindow() uint32 {
	w := s.windowSize
	if uint32(s.recvWindow) < w {
		w = uint32(s.recvWindow)
	}
	return w
}

func (s *Sender) inflightBytes() uint32 {
	var total uint32
	for _, r := range s.inflight {
		total += r.length
	}
	return total
}

func (s *Sender) sendLoop() {
	log.Printf("[RDT] sender loop started, session=%d", s.sessionID)
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

		offset := 0
		for offset < len(data) {
			s.mu.Lock()
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

			end := offset + 1400
			if end > len(data) {
				end = len(data)
			}
			chunk := data[offset:end]
			chunkLen := uint32(len(chunk))

			seq := s.nextSeqNum
			s.nextSeqNum += chunkLen

			h := &Header{
				SessionID:  s.sessionID,
				SeqNum:     seq,
				AckNum:     0,
				Flags:      FlagDATA,
				PayloadLen: uint16(chunkLen),
				Window:     65535,
			}
			encoded, _ := h.Encode(chunk)
			checksum := ComputeChecksum(encoded)
			binary.BigEndian.PutUint32(encoded[17:21], checksum)

			if _, err := s.conn.WriteToUDP(encoded, s.remoteAddr); err != nil {
				log.Printf("[RDT] sender write error: %v", err)
				s.mu.Unlock()
				return
			}

			s.inflight[seq] = &packetRecord{
				data:   encoded,
				seq:    seq,
				length: chunkLen,
				sentAt: time.Now(),
			}
			s.timers.Start(seq)
			s.mu.Unlock()

			offset = end
		}
	}
}

func (s *Sender) recvLoop() {
	log.Printf("[RDT] sender recv loop started, session=%d", s.sessionID)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := s.conn.ReadFromUDP(buf)
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

		if hdr.SessionID != s.sessionID {
			continue
		}

		s.mu.Lock()

		switch {
		case hdr.Flags&FlagRST != 0:
			log.Printf("[RDT] sender got RST")
			s.state = sendClosed
			s.mu.Unlock()
			close(s.done)
			return

		case hdr.Flags&(FlagFIN|FlagACK) == FlagFIN|FlagACK:
			s.state = sendClosed
			s.mu.Unlock()
			close(s.done)
			return

		case hdr.Flags&FlagACK != 0:
			s.recvWindow = hdr.Window
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

	for seq := s.sendBase; seq < ackNum; {
		if rec, ok := s.inflight[seq]; ok {
			rec.done = true
			s.timers.Cancel(seq)
			delete(s.inflight, seq)
			seq += rec.length
		} else {
			seq++
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
