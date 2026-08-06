package rdt

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const HeaderSize = 17

type UDPConn interface {
	ReadFromUDP(b []byte) (int, *net.UDPAddr, error)
	WriteToUDP(b []byte, addr *net.UDPAddr) (int, error)
	Close() error
	LocalAddr() net.Addr
	SetReadDeadline(t time.Time) error
}

const (
	FlagDATA uint8 = 0x01
	FlagFIN        = 0x02
	FlagACK        = 0x04
)

func WindowScale(flags uint8) uint8 {
	return (flags >> 5) & 0x07
}

func SetWindowScale(flags uint8, scale uint8) uint8 {
	return (flags & 0x1F) | ((scale & 0x07) << 5)
}

type Header struct {
	SeqNum     uint32
	AckNum     uint32
	Flags      uint8
	PayloadLen uint16
	Window     uint16
	Checksum   uint32
}

func (h *Header) EffectiveWindow() uint32 {
	return uint32(h.Window) << WindowScale(h.Flags)
}

func (h *Header) SetWindow(win uint32, scale uint8) {
	h.Window = uint16(win >> scale)
	h.Flags = SetWindowScale(h.Flags, scale)
}

func (h *Header) Encode(payload []byte) ([]byte, error) {
	h.PayloadLen = uint16(len(payload))
	buf := make([]byte, HeaderSize+len(payload))

	binary.BigEndian.PutUint32(buf[0:4], h.SeqNum)
	binary.BigEndian.PutUint32(buf[4:8], h.AckNum)
	buf[8] = h.Flags
	binary.BigEndian.PutUint16(buf[9:11], h.PayloadLen)
	binary.BigEndian.PutUint16(buf[11:13], h.Window)
	binary.BigEndian.PutUint32(buf[13:17], h.Checksum)

	copy(buf[HeaderSize:], payload)
	return buf, nil
}

func Decode(data []byte) (*Header, []byte, error) {
	if len(data) < HeaderSize {
		return nil, nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}
	h := &Header{
		SeqNum:     binary.BigEndian.Uint32(data[0:4]),
		AckNum:     binary.BigEndian.Uint32(data[4:8]),
		Flags:      data[8],
		PayloadLen: binary.BigEndian.Uint16(data[9:11]),
		Window:     binary.BigEndian.Uint16(data[11:13]),
		Checksum:   binary.BigEndian.Uint32(data[13:17]),
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
