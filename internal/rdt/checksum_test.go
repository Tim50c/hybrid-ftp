package rdt

import (
	"encoding/binary"
	"testing"
)

func TestComputeAndVerify_Success(t *testing.T) {
	h := &Header{
		SeqNum: 100,
		AckNum: 200,
		Flags:  FlagDATA,
		Window: 65535,
	}
	payload := []byte("test payload for checksum verification")
	encoded, _ := h.Encode(payload)

	originalChecksum := binary.BigEndian.Uint32(encoded[13:17])
	t.Logf("original checksum field (before compute): %08x", originalChecksum)

	checksum := ComputeChecksum(encoded)
	t.Logf("computed checksum: %08x", checksum)

	binary.BigEndian.PutUint32(encoded[13:17], checksum)

	if !VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return true for valid packet")
	}
}

func TestVerifyChecksum_Corruption(t *testing.T) {
	h := &Header{
		SeqNum: 100,
	}
	payload := []byte("data")
	encoded, _ := h.Encode(payload)

	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)

	encoded[HeaderSize] ^= 0xFF

	if VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return false for corrupted packet")
	}
}

func TestComputeChecksum_Idempotent(t *testing.T) {
	h := &Header{}
	payload := []byte("consistent")
	encoded, _ := h.Encode(payload)

	c1 := ComputeChecksum(encoded)
	c2 := ComputeChecksum(encoded)
	if c1 != c2 {
		t.Error("ComputeChecksum should produce same result for same input")
	}
}

func TestVerifyChecksum_ZeroChecksum(t *testing.T) {
	h := &Header{}
	payload := []byte("x")
	encoded, _ := h.Encode(payload)
	binary.BigEndian.PutUint32(encoded[13:17], 0)

	if VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return false when checksum field is zero (not yet set)")
	}
}

func TestVerifyChecksum_EmptyPacket(t *testing.T) {
	h := &Header{
		SeqNum:     0,
		Flags:      FlagACK,
		PayloadLen: 0,
		Window:     65535,
	}
	encoded, _ := h.Encode(nil)
	checksum := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], checksum)

	if !VerifyChecksum(encoded) {
		t.Error("VerifyChecksum should return true for valid ACK-only packet")
	}
}
