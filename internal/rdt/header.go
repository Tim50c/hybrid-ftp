package rdt

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const HeaderSize = 21

type UDPConn interface {
	ReadFromUDP(b []byte) (int, *net.UDPAddr, error)
	WriteToUDP(b []byte, addr *net.UDPAddr) (int, error)
	Close() error
	LocalAddr() net.Addr
	SetReadDeadline(t time.Time) error
}

const (
	FlagSYN  uint8 = 0x01
	FlagFIN        = 0x02
	FlagACK        = 0x04
	FlagRST        = 0x08
	FlagDATA       = 0x10
)

type Header struct {
	SessionID  uint32
	SeqNum     uint32
	AckNum     uint32
	Flags      uint8
	PayloadLen uint16
	Window     uint16
	Checksum   uint32
}

func (h *Header) Encode(payload []byte) ([]byte, error) {
	h.PayloadLen = uint16(len(payload))
	buf := make([]byte, HeaderSize+len(payload))

	binary.BigEndian.PutUint32(buf[0:4], h.SessionID)
	binary.BigEndian.PutUint32(buf[4:8], h.SeqNum)
	binary.BigEndian.PutUint32(buf[8:12], h.AckNum)
	buf[12] = h.Flags
	binary.BigEndian.PutUint16(buf[13:15], h.PayloadLen)
	binary.BigEndian.PutUint16(buf[15:17], h.Window)
	binary.BigEndian.PutUint32(buf[17:21], h.Checksum)

	copy(buf[HeaderSize:], payload)
	return buf, nil
}

func Decode(data []byte) (*Header, []byte, error) {
	if len(data) < HeaderSize {
		return nil, nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}
	h := &Header{
		SessionID:  binary.BigEndian.Uint32(data[0:4]),
		SeqNum:     binary.BigEndian.Uint32(data[4:8]),
		AckNum:     binary.BigEndian.Uint32(data[8:12]),
		Flags:      data[12],
		PayloadLen: binary.BigEndian.Uint16(data[13:15]),
		Window:     binary.BigEndian.Uint16(data[15:17]),
		Checksum:   binary.BigEndian.Uint32(data[17:21]),
	}

	expectedTotal := HeaderSize + int(h.PayloadLen)
	if len(data) < expectedTotal {
		return nil, nil, fmt.Errorf("expected %d bytes for payload, got %d", expectedTotal, len(data))
	}

	var payload []byte
	if h.PayloadLen > 0 {
		payload = data[HeaderSize:expectedTotal]
	}
	return h, payload, nil
}
