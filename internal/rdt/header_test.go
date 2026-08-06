package rdt

import (
	"testing"
)

func TestHeaderEncodeDecode_RoundTrip(t *testing.T) {
	payload := []byte("hello world payload data")
	h := &Header{
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
	buf[9] = 0
	buf[10] = 100
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
	if FlagDATA != 0x01 {
		t.Errorf("FlagDATA = %02x, want 0x01", FlagDATA)
	}
	if FlagFIN != 0x02 {
		t.Errorf("FlagFIN = %02x, want 0x02", FlagFIN)
	}
	if FlagACK != 0x04 {
		t.Errorf("FlagACK = %02x, want 0x04", FlagACK)
	}
}

func TestHeader_EffectiveWindow(t *testing.T) {
	tests := []struct {
		raw   uint16
		scale uint8
		want  uint32
	}{
		{65535, 0, 65535},
		{16000, 2, 64000},
		{1, 5, 32},
		{32768, 3, 262144},
	}
	for _, tt := range tests {
		h := &Header{Window: tt.raw}
		h.Flags = SetWindowScale(h.Flags, tt.scale)
		got := h.EffectiveWindow()
		if got != tt.want {
			t.Errorf("raw=%d scale=%d: EffectiveWindow()=%d, want %d", tt.raw, tt.scale, got, tt.want)
		}
	}
}

func TestHeaderEncodeDecode_AllFlagCombos(t *testing.T) {
	flagCombos := []uint8{
		FlagDATA,
		FlagACK,
		FlagDATA | FlagACK,
		FlagFIN,
		FlagFIN | FlagACK,
	}
	payload := []byte("flag combo test data")
	for _, flags := range flagCombos {
		h := &Header{
			SeqNum: 0,
			AckNum: 200,
			Flags:  flags,
			Window: 64000,
		}
		encoded, err := h.Encode(payload)
		if err != nil {
			t.Fatalf("Encode flags=0x%02x: %v", flags, err)
		}
		decoded, _, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode flags=0x%02x: %v", flags, err)
		}
		if decoded.Flags != flags {
			t.Errorf("Flags mismatch: got 0x%02x, want 0x%02x", decoded.Flags, flags)
		}
	}
}

func TestHeader_SetWindow(t *testing.T) {
	h := &Header{}
	h.SetWindow(256000, 2)
	if h.Window != 64000 {
		t.Errorf("Window = %d, want 64000", h.Window)
	}
	if WindowScale(h.Flags) != 2 {
		t.Errorf("WindowScale = %d, want 2", WindowScale(h.Flags))
	}
	if h.EffectiveWindow() != 256000 {
		t.Errorf("EffectiveWindow = %d, want 256000", h.EffectiveWindow())
	}
}

func TestHeaderDecode_ReservedBitsIgnored(t *testing.T) {
	h := &Header{Flags: FlagDATA | 0xE0, Window: 65535}
	encoded, _ := h.Encode(nil)
	decoded, _, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode with reserved bits: %v", err)
	}
	if decoded.Flags&FlagDATA != FlagDATA {
		t.Error("DATA flag should survive reserved bits")
	}
}

func TestHeaderDecode_AllZeros(t *testing.T) {
	h := &Header{SeqNum: 0, AckNum: 0, Flags: 0, PayloadLen: 0, Window: 0}
	encoded, _ := h.Encode(nil)
	decoded, _, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode all-zeros: %v", err)
	}
	if decoded.Flags != 0 {
		t.Error("all-zeros header should decode correctly")
	}
}

func TestHeaderEncodeDecode_MaxValues(t *testing.T) {
	payload := []byte("max vals")
	h := &Header{
		SeqNum:     0xFFFFFFFF,
		AckNum:     0xFFFFFFFF,
		Flags:      FlagDATA | FlagACK | FlagFIN,
		PayloadLen: uint16(len(payload)),
		Window:     0xFFFF,
	}
	encoded, _ := h.Encode(payload)
	decoded, decPayload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode max values: %v", err)
	}
	if decoded.SeqNum != 0xFFFFFFFF {
		t.Errorf("SeqNum: got 0x%08X", decoded.SeqNum)
	}
	if decoded.AckNum != 0xFFFFFFFF {
		t.Errorf("AckNum: got 0x%08X", decoded.AckNum)
	}
	if decoded.Window != 0xFFFF {
		t.Errorf("Window: got 0x%04X", decoded.Window)
	}
	if string(decPayload) != string(payload) {
		t.Errorf("payload mismatch: %q vs %q", decPayload, payload)
	}
}

func TestHeaderEncode_PayloadLenAutoSet(t *testing.T) {
	h := &Header{}
	payload := []byte("auto payload length")
	encoded, err := h.Encode(payload)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, _, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.PayloadLen != uint16(len(payload)) {
		t.Errorf("PayloadLen auto-set: got %d, want %d", decoded.PayloadLen, len(payload))
	}
}

func TestHeaderDecode_ExactFit(t *testing.T) {
	h := &Header{SeqNum: 100, Flags: FlagDATA}
	payload := []byte("exact")
	encoded, _ := h.Encode(payload)

	for i := 0; i < len(encoded); i++ {
		short := encoded[:i]
		_, _, err := Decode(short)
		if i < HeaderSize {
			if err == nil {
				t.Errorf("expected error for %d-byte buffer (< HeaderSize)", i)
			}
		} else if i < len(encoded) {
			if err == nil {
				t.Errorf("expected error for %d-byte buffer (payload cut short)", i)
			}
		}
	}
}
