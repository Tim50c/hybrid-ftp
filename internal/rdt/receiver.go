package rdt

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type receiverState int

const (
	recvEstablished receiverState = iota
	recvFinWaiting
)

type Receiver struct {
	conn       UDPConn
	remoteAddr *net.UDPAddr
	mu         sync.Mutex
	state      receiverState
	rcvBase    uint32
	recvBuffer map[uint32][]byte
	recvWindow uint32
	myScale    uint8
	maxBuffer  int
	output     chan []byte
	outputDone bool
	done       chan struct{}
	closeOnce  sync.Once
	recvReady  chan struct{}
}

func NewReceiver(conn UDPConn) *Receiver {
	const baseWindow uint32 = 256 * 1024
	scale := uint8(0)
	for (uint32(65535) << scale) < baseWindow && scale < 7 {
		scale++
	}
	return &Receiver{
		conn:       conn,
		state:      recvEstablished,
		recvBuffer: make(map[uint32][]byte),
		recvWindow: baseWindow,
		myScale:    scale,
		maxBuffer:  2000,
		output:     make(chan []byte, 5000),
		done:       make(chan struct{}),
		recvReady:  make(chan struct{}),
	}
}

func (r *Receiver) Start(addr *net.UDPAddr) error {
	r.mu.Lock()
	r.remoteAddr = addr
	r.state = recvEstablished
	r.mu.Unlock()

	h := &Header{
		AckNum: 0,
		Flags:  FlagACK,
	}
	h.SetWindow(r.recvWindow, r.myScale)
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode ready ack: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)
	r.conn.WriteToUDP(encoded, r.remoteAddr)

	go r.receiveLoop()
	return nil
}

func (r *Receiver) Accept() error {
	r.mu.Lock()
	r.state = recvEstablished
	recvReady := r.recvReady
	r.mu.Unlock()

	go r.receiveLoop()

	select {
	case <-recvReady:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("accept timed out waiting for peer")
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

func (r *Receiver) sendControl(flags uint8) error {
	h := &Header{
		SeqNum:     0,
		AckNum:     r.rcvBase,
		Flags:      0,
		PayloadLen: 0,
	}
	h.SetWindow(r.recvWindow, r.myScale)
	h.Flags |= flags
	encoded, err := h.Encode(nil)
	if err != nil {
		return fmt.Errorf("encode control: %w", err)
	}
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)

	_, err = r.conn.WriteToUDP(encoded, r.remoteAddr)
	if err != nil {
		return fmt.Errorf("send control: %w", err)
	}
	return nil
}

func (r *Receiver) sendAck(ackNum uint32) {
	avail := r.windowAvailable()
	h := &Header{
		SeqNum:     0,
		AckNum:     ackNum,
		Flags:      0,
		PayloadLen: 0,
	}
	h.SetWindow(uint32(avail), r.myScale)
	h.Flags |= FlagACK
	encoded, _ := h.Encode(nil)
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)
	r.conn.WriteToUDP(encoded, r.remoteAddr)
}

func (r *Receiver) windowAvailable() uint32 {
	buffered := uint32(len(r.recvBuffer) * 1400)
	if buffered > r.recvWindow {
		return 0
	}
	return r.recvWindow - buffered
}

func (r *Receiver) receiveLoop() {
	log.Printf("[RDT] receiver loop started")
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
			r.sendControl(FlagFIN | FlagACK)
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
		n, addr, err := r.conn.ReadFromUDP(buf)
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

		r.mu.Lock()

		if r.remoteAddr == nil {
			r.remoteAddr = addr
			if r.recvReady != nil {
				close(r.recvReady)
				r.recvReady = nil
			}
		}

		switch {
		case hdr.Flags&FlagFIN != 0:
			log.Printf("[RDT] receiver got FIN")
			r.state = recvFinWaiting
			r.sendControl(FlagFIN | FlagACK)
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

		case hdr.Flags&FlagACK != 0:
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
			}
		}
		r.sendAck(r.rcvBase)
	}
}
