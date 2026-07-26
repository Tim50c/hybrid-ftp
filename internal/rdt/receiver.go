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

type receiverState int

const (
	recvClosed receiverState = iota
	recvAccepting
	recvEstablished
	recvFinWaiting
)

type Receiver struct {
	conn        UDPConn
	sessionID   uint32
	remoteAddr  *net.UDPAddr
	mu          sync.Mutex
	state       receiverState
	rcvBase     uint32
	recvBuffer  map[uint32][]byte
	recvWindow  uint16
	maxBuffer   int
	output      chan []byte
	outputDone  bool
	done        chan struct{}
	closeOnce   sync.Once
}

func NewReceiver(conn UDPConn, sessionID uint32) *Receiver {
	return &Receiver{
		conn:       conn,
		sessionID:  sessionID,
		state:      recvClosed,
		recvBuffer: make(map[uint32][]byte),
		recvWindow: 65535,
		maxBuffer:  1000,
		output:     make(chan []byte, 100),
		done:       make(chan struct{}),
	}
}

func (r *Receiver) Accept() error {
	r.mu.Lock()
	r.state = recvAccepting
	r.mu.Unlock()

	r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		buf := make([]byte, 1500)
		n, addr, err := r.conn.ReadFromUDP(buf)
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

		if hdr.SessionID != r.sessionID || hdr.Flags != FlagSYN {
			continue
		}

		r.mu.Lock()
		r.remoteAddr = addr
		r.rcvBase = hdr.SeqNum + 1
		myISN := uint32(rand.Int31())
		r.sendControl(myISN, r.rcvBase, FlagSYN|FlagACK)
		r.mu.Unlock()

		for {
			buf := make([]byte, 1500)
			n, _, err := r.conn.ReadFromUDP(buf)
			if err != nil {
				return fmt.Errorf("third ack read: %w", err)
			}
			hdr, _, err := Decode(buf[:n])
			if err != nil {
				continue
			}
			if !VerifyChecksum(buf[:n]) {
				continue
			}
			if hdr.SessionID != r.sessionID || hdr.Flags != FlagACK {
				continue
			}
			break
		}

		r.mu.Lock()
		r.state = recvEstablished
		r.mu.Unlock()

		go r.receiveLoop()
		return nil
	}
}

func (r *Receiver) Connect(addr *net.UDPAddr) error {
	r.mu.Lock()
	r.remoteAddr = addr
	r.mu.Unlock()

	myISN := uint32(rand.Int31())
	if err := r.sendControl(myISN, 0, FlagSYN); err != nil {
		return fmt.Errorf("connect syn: %w", err)
	}

	for {
		buf := make([]byte, 1500)
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("connect read synack: %w", err)
		}
		hdr, _, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		if !VerifyChecksum(buf[:n]) {
			continue
		}
		if hdr.SessionID != r.sessionID || hdr.Flags != (FlagSYN|FlagACK) {
			continue
		}

		r.mu.Lock()
		r.rcvBase = hdr.SeqNum + 1
		if err := r.sendControl(0, r.rcvBase, FlagACK); err != nil {
			r.mu.Unlock()
			return fmt.Errorf("connect ack: %w", err)
		}
		r.state = recvEstablished
		r.mu.Unlock()

		go r.receiveLoop()
		return nil
	}
}

func (r *Receiver) Receive() <-chan []byte {
	return r.output
}

func (r *Receiver) Close() error {
	r.closeOnce.Do(func() {
		close(r.done)
	})
	return nil
}

func (r *Receiver) sendControl(seqNum, ackNum uint32, flags uint8) error {
	h := &Header{
		SessionID:  r.sessionID,
		SeqNum:     seqNum,
		AckNum:     ackNum,
		Flags:      flags,
		PayloadLen: 0,
		Window:     r.recvWindow,
	}
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode control: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)

	_, err = r.conn.WriteToUDP(encoded, r.remoteAddr)
	if err != nil {
		return fmt.Errorf("send control: %w", err)
	}
	return nil
}

func (r *Receiver) sendAck(ackNum uint32) {
	h := &Header{
		SessionID:  r.sessionID,
		SeqNum:     0,
		AckNum:     ackNum,
		Flags:      FlagACK,
		PayloadLen: 0,
		Window:     r.windowAvailable(),
	}
	encoded, _ := h.Encode(nil)
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], checksum)
	r.conn.WriteToUDP(encoded, r.remoteAddr)
}

func (r *Receiver) windowAvailable() uint16 {
	buffered := uint16(len(r.recvBuffer) * 1400)
	if buffered > r.recvWindow {
		return 0
	}
	return r.recvWindow - buffered
}

func (r *Receiver) receiveLoop() {
	log.Printf("[RDT] receiver loop started, session=%d", r.sessionID)
	var postFinDeadline time.Time
	defer func() {
		r.mu.Lock()
		var flushChunks [][]byte
		for {
			chunk, ok := r.recvBuffer[r.rcvBase]
			if !ok {
				break
			}
			flushChunks = append(flushChunks, chunk)
			r.rcvBase += uint32(len(chunk))
			delete(r.recvBuffer, r.rcvBase-uint32(len(chunk)))
		}
		r.mu.Unlock()

		for _, chunk := range flushChunks {
			select {
			case r.output <- chunk:
			case <-r.done:
				goto closed
			}
		}
	closed:
		r.mu.Lock()
		if r.state == recvFinWaiting {
			r.sendControl(0, r.rcvBase, FlagFIN|FlagACK)
			time.Sleep(100 * time.Millisecond)
		}
		if !r.outputDone {
			close(r.output)
			r.outputDone = true
		}
		r.mu.Unlock()
	}()
	for {
		select {
		case <-r.done:
			return
		default:
		}

		r.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if !postFinDeadline.IsZero() && time.Now().After(postFinDeadline) {
					return
				}
				continue
			}
			log.Printf("[RDT] receiver read error: %v", err)
			return
		}

		if !VerifyChecksum(buf[:n]) {
			log.Printf("[RDT] receiver checksum fail")
			continue
		}

		hdr, payload, err := Decode(buf[:n])
		if err != nil {
			log.Printf("[RDT] receiver decode error: %v", err)
			continue
		}

		if hdr.SessionID != r.sessionID {
			continue
		}

		r.mu.Lock()

		switch {
		case hdr.Flags&FlagRST != 0:
			log.Printf("[RDT] receiver got RST, closing")
			r.state = recvClosed
			r.mu.Unlock()
			r.Close()
			return

		case hdr.Flags&FlagFIN != 0:
			log.Printf("[RDT] receiver got FIN")
			r.state = recvFinWaiting
			r.sendControl(0, r.rcvBase, FlagFIN|FlagACK)
			r.mu.Unlock()
			r.mu.Lock()
			hasBuffered := len(r.recvBuffer) > 0
			r.mu.Unlock()
			if !hasBuffered {
				return
			}
			postFinDeadline = time.Now().Add(4 * time.Second)

		case hdr.Flags&FlagDATA != 0:
			r.handleData(hdr, payload)
			r.mu.Unlock()

		default:
			r.mu.Unlock()
		}
	}
}

func (r *Receiver) handleData(hdr *Header, payload []byte) {
	if hdr.SeqNum < r.rcvBase {
		r.sendAck(r.rcvBase)
		return
	}

	dataLen := uint32(len(payload))
	if hdr.SeqNum == r.rcvBase {
		r.rcvBase += dataLen

		bufferedChunks := make([][]byte, 0)
		for {
			buffered, ok := r.recvBuffer[r.rcvBase]
			if !ok {
				break
			}
			bufferedChunks = append(bufferedChunks, buffered)
			r.rcvBase += uint32(len(buffered))
			delete(r.recvBuffer, r.rcvBase-uint32(len(buffered)))
		}

		r.mu.Unlock()
		r.output <- payload
		for _, chunk := range bufferedChunks {
			r.output <- chunk
		}
		r.mu.Lock()

		r.sendAck(r.rcvBase)
	} else if hdr.SeqNum > r.rcvBase {
		if _, exists := r.recvBuffer[hdr.SeqNum]; !exists {
			if len(r.recvBuffer) < r.maxBuffer {
				r.recvBuffer[hdr.SeqNum] = payload
				log.Printf("[RDT] buffered out-of-order seq=%d (rcvBase=%d)", hdr.SeqNum, r.rcvBase)
			}
		}
		r.sendAck(r.rcvBase)
	}
}
