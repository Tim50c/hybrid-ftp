package rdt

import (
	"testing"
)

func TestHeaderEncodeDecode_RoundTrip(t *testing.T) {
	payload := []byte("hello world payload data")
	h := &Header{
		SessionID:  42,
		SeqNum:     1234,
		AckNum:     5678,
		Flags:      FlagDATA | FlagACK,
		PayloadLen: uint16(len(payload)),
		Window:     64000,
		Checksum:   0,
	}

	encoded, err := h.Encode(payload)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	expectedLen := HeaderSize + len(payload)
	if len(encoded) != expectedLen {
		t.Errorf("encoded length = %d, want %d", len(encoded), expectedLen)
	}

	decoded, decodedPayload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if decoded.SessionID != h.SessionID {
		t.Errorf("SessionID = %d, want %d", decoded.SessionID, h.SessionID)
	}
	if decoded.SeqNum != h.SeqNum {
		t.Errorf("SeqNum = %d, want %d", decoded.SeqNum, h.SeqNum)
	}
	if decoded.AckNum != h.AckNum {
		t.Errorf("AckNum = %d, want %d", decoded.AckNum, h.AckNum)
	}
	if decoded.Flags != h.Flags {
		t.Errorf("Flags = %02x, want %02x", decoded.Flags, h.Flags)
	}
	if decoded.PayloadLen != h.PayloadLen {
		t.Errorf("PayloadLen = %d, want %d", decoded.PayloadLen, h.PayloadLen)
	}
	if decoded.Window != h.Window {
		t.Errorf("Window = %d, want %d", decoded.Window, h.Window)
	}
	if string(decodedPayload) != string(payload) {
		t.Errorf("payload mismatch: %q vs %q", decodedPayload, payload)
	}
}

func TestHeaderEncodeDecode_NoPayload(t *testing.T) {
	h := &Header{
		SessionID:  1,
		SeqNum:     0,
		AckNum:     100,
		Flags:      FlagACK,
		PayloadLen: 0,
		Window:     65535,
	}

	encoded, err := h.Encode(nil)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(encoded) != HeaderSize {
		t.Errorf("encoded length = %d, want %d", len(encoded), HeaderSize)
	}

	decoded, payload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if payload != nil {
		t.Errorf("payload should be nil for zero-length data")
	}
	if decoded.Flags != FlagACK {
		t.Errorf("Flags = %02x, want %02x", decoded.Flags, FlagACK)
	}
}

func TestHeaderDecode_TooShort(t *testing.T) {
	short := make([]byte, HeaderSize-1)
	_, _, err := Decode(short)
	if err == nil {
		t.Error("Decode should fail on short buffer")
	}
}

func TestHeaderDecode_PayloadLengthMismatch(t *testing.T) {
	buf := make([]byte, HeaderSize+50)
	buf[13] = 0
	buf[14] = 100
	_, _, err := Decode(buf)
	if err == nil {
		t.Error("Decode should fail on payload length mismatch")
	}
}

func TestHeaderDecode_LargePayload(t *testing.T) {
	payload := make([]byte, 1400)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	h := &Header{
		SessionID:  999,
		SeqNum:     1400,
		Flags:      FlagDATA,
		PayloadLen: 1400,
		Window:     32000,
	}
	encoded, _ := h.Encode(payload)
	decoded, decPayload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if decoded.PayloadLen != 1400 {
		t.Errorf("PayloadLen = %d, want 1400", decoded.PayloadLen)
	}
	if len(decPayload) != 1400 {
		t.Errorf("decoded payload length = %d, want 1400", len(decPayload))
	}
}

func TestFlags_Const(t *testing.T) {
	if FlagSYN != 0x01 {
		t.Errorf("FlagSYN = %02x, want 0x01", FlagSYN)
	}
	if FlagFIN != 0x02 {
		t.Errorf("FlagFIN = %02x, want 0x02", FlagFIN)
	}
	if FlagACK != 0x04 {
		t.Errorf("FlagACK = %02x, want 0x04", FlagACK)
	}
	if FlagRST != 0x08 {
		t.Errorf("FlagRST = %02x, want 0x08", FlagRST)
	}
	if FlagDATA != 0x10 {
		t.Errorf("FlagDATA = %02x, want 0x10", FlagDATA)
	}
}
