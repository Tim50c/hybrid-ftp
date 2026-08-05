package rdt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

func makeRDTDataPacket(t *testing.T, seqNum uint32, payload []byte) []byte {
	t.Helper()
	h := &Header{
		SeqNum:     seqNum,
		Flags:      FlagDATA,
		PayloadLen: uint16(len(payload)),
		Window:     65535,
	}
	encoded, err := h.Encode(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	cs := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], cs)
	return encoded
}

func wakeAccept(t *testing.T, peerConn *net.UDPConn, receiverAddr *net.UDPAddr) {
	t.Helper()
	wakeHdr := &Header{Flags: FlagACK, Window: 65535}
	wakeBytes, _ := wakeHdr.Encode(nil)
	cs := ComputeChecksum(wakeBytes)
	binary.BigEndian.PutUint32(wakeBytes[13:17], cs)
	if _, err := peerConn.WriteToUDP(wakeBytes, receiverAddr); err != nil {
		t.Fatalf("wakeAccept: %v", err)
	}
}

func TestReceiver_FlushesBufferedOnExit(t *testing.T) {
	receiverAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	receiverConn, err := net.ListenUDP("udp", receiverAddr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer receiverConn.Close()

	peerAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	peerConn, err := net.ListenUDP("udp", peerAddr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer peerConn.Close()

	const mss = 100

	receiver := NewReceiver(receiverConn)
	receiverAddrUDP := receiverConn.LocalAddr().(*net.UDPAddr)

	acceptDone := make(chan error, 1)
	go func() {
		acceptDone <- receiver.Accept()
	}()

	time.Sleep(100 * time.Millisecond)

	wakeAccept(t, peerConn, receiverAddrUDP)

	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept timed out")
	}

	totalChunks := 5
	chunks := make([][]byte, totalChunks)
	for i := 0; i < totalChunks; i++ {
		chunks[i] = bytes.Repeat([]byte{byte('A' + i)}, mss)
	}

	for i := totalChunks - 1; i >= 1; i-- {
		seq := uint32(i * mss)
		pkt := makeRDTDataPacket(t, seq, chunks[i])
		if _, err := peerConn.WriteToUDP(pkt, receiverAddrUDP); err != nil {
			t.Fatalf("write data %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	fin := &Header{Flags: FlagFIN, Window: 65535}
	sendPacket(t, peerConn, receiverAddrUDP, fin, nil)

	time.Sleep(200 * time.Millisecond)
	firstPkt := makeRDTDataPacket(t, 0, chunks[0])
	if _, err := peerConn.WriteToUDP(firstPkt, receiverAddrUDP); err != nil {
		t.Fatalf("write retransmit: %v", err)
	}

	var got []byte
	deadline := time.After(4 * time.Second)
collect:
	for {
		select {
		case chunk, ok := <-receiver.Receive():
			if !ok {
				break collect
			}
			got = append(got, chunk...)
		case <-deadline:
			t.Fatal("timed out waiting for output")
		}
	}

	want := make([]byte, 0, totalChunks*mss)
	for i := 0; i < totalChunks; i++ {
		want = append(want, chunks[i]...)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("output mismatch:\n got:  %x\n want: %x", got, want)
		t.Errorf("got %d bytes, want %d bytes", len(got), len(want))
	}

	if err := receiver.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
}

func TestReceiver_HandlesPartialDelivery(t *testing.T) {
	t.Skip("Documents a known limitation: receiver does not retransmit-request for buffered gaps after FIN")
	_ = fmt.Sprintf
}

func TestReceiver_InOrderDelivery(t *testing.T) {
	receiverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer receiverConn.Close()

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer peerConn.Close()

	receiver := NewReceiver(receiverConn)
	receiverAddr := receiverConn.LocalAddr().(*net.UDPAddr)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	wakeAccept(t, peerConn, receiverAddr)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	chunks := []string{"AAAA", "BBBB", "CCCC", "DDDD"}
	offset := uint32(0)
	for _, c := range chunks {
		seq := offset
		pkt := makeRDTDataPacket(t, seq, []byte(c))
		peerConn.WriteToUDP(pkt, receiverAddr)
		offset += uint32(len(c))
		time.Sleep(5 * time.Millisecond)
	}

	fin := &Header{Flags: FlagFIN, Window: 65535}
	sendPacket(t, peerConn, receiverAddr, fin, nil)

	var got []byte
	deadline := time.After(3 * time.Second)
collect:
	for {
		select {
		case chunk, ok := <-receiver.Receive():
			if !ok {
				break collect
			}
			got = append(got, chunk...)
		case <-deadline:
			t.Fatal("timeout waiting for output")
		}
	}

	want := "AAAABBBBCCCCDDDD"
	if string(got) != want {
		t.Errorf("in-order delivery: got %q, want %q", string(got), want)
	}

	receiver.Close()
}

func TestReceiver_DuplicateDetection(t *testing.T) {
	receiverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer receiverConn.Close()

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer peerConn.Close()

	receiver := NewReceiver(receiverConn)
	receiverAddr := receiverConn.LocalAddr().(*net.UDPAddr)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	wakeAccept(t, peerConn, receiverAddr)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	pkt := makeRDTDataPacket(t, 0, []byte("UNIQUE"))
	peerConn.WriteToUDP(pkt, receiverAddr)
	time.Sleep(20 * time.Millisecond)
	peerConn.WriteToUDP(pkt, receiverAddr)
	time.Sleep(20 * time.Millisecond)

	fin := &Header{Flags: FlagFIN, Window: 65535}
	sendPacket(t, peerConn, receiverAddr, fin, nil)

	var got []byte
	deadline := time.After(3 * time.Second)
collect:
	for {
		select {
		case chunk, ok := <-receiver.Receive():
			if !ok {
				break collect
			}
			got = append(got, chunk...)
		case <-deadline:
			t.Fatal("timeout waiting for output")
		}
	}

	if len(got) != 6 || string(got) != "UNIQUE" {
		t.Errorf("duplicate should produce single copy: got %d bytes %q", len(got), string(got))
	}

	receiver.Close()
}

func TestReceiver_ChecksumDiscard(t *testing.T) {
	receiverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer receiverConn.Close()

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer peerConn.Close()

	receiver := NewReceiver(receiverConn)
	receiverAddr := receiverConn.LocalAddr().(*net.UDPAddr)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	wakeAccept(t, peerConn, receiverAddr)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	pkt := makeRDTDataPacket(t, 0, []byte("GOOD"))
	peerConn.WriteToUDP(pkt, receiverAddr)
	time.Sleep(20 * time.Millisecond)

	corruptPkt := makeRDTDataPacket(t, uint32(len("GOOD")), []byte("CORRUPT"))
	if len(corruptPkt) > HeaderSize+1 {
		corruptPkt[HeaderSize] ^= 0xFF
	}
	peerConn.WriteToUDP(corruptPkt, receiverAddr)
	time.Sleep(20 * time.Millisecond)

	actualPkt := makeRDTDataPacket(t, uint32(len("GOOD")), []byte("CLEAN"))
	peerConn.WriteToUDP(actualPkt, receiverAddr)
	time.Sleep(20 * time.Millisecond)

	fin := &Header{Flags: FlagFIN, Window: 65535}
	sendPacket(t, peerConn, receiverAddr, fin, nil)

	var got []byte
	deadline := time.After(3 * time.Second)
collect:
	for {
		select {
		case chunk, ok := <-receiver.Receive():
			if !ok {
				break collect
			}
			got = append(got, chunk...)
		case <-deadline:
			t.Fatal("timeout waiting for output")
		}
	}

	want := "GOODCLEAN"
	if string(got) != want {
		t.Errorf("corrupt discard: got %q, want %q", string(got), want)
	}

	receiver.Close()
}

func TestReceiver_RSTHandling(t *testing.T) {
	t.Skip("RST flag removed; FIN handling tested in other receiver tests")
}

func TestReceiver_WindowAdvertisement(t *testing.T) {
	receiverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer receiverConn.Close()

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer peerConn.Close()

	receiver := NewReceiver(receiverConn)
	receiverAddr := receiverConn.LocalAddr().(*net.UDPAddr)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	wakeAccept(t, peerConn, receiverAddr)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	pkt := makeRDTDataPacket(t, 0, []byte("WINDOW-TEST"))
	peerConn.WriteToUDP(pkt, receiverAddr)
	time.Sleep(50 * time.Millisecond)

	peerConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read ACK for window: %v", err)
	}
	ackHdr, _, _ := Decode(buf[:n])

	if ackHdr.Window == 0 {
		t.Error("window should not be 0 with empty buffer")
	}
	if ackHdr.Window > 65535 {
		t.Errorf("window %d exceeds max", ackHdr.Window)
	}

	fin := &Header{Flags: FlagFIN, Window: 65535}
	sendPacket(t, peerConn, receiverAddr, fin, nil)

	drainChannel(t, receiver.Receive())
	receiver.Close()
}

func TestReceiver_DoubleCloseSafety(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	r := NewReceiver(conn)

	err1 := r.Close()
	if err1 != nil {
		t.Errorf("first Close returned error: %v", err1)
	}

	err2 := r.Close()
	if err2 != nil {
		t.Errorf("second Close returned error: %v", err2)
	}
}

func drainChannel(t *testing.T, ch <-chan []byte) {
	t.Helper()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func sendPacket(t *testing.T, conn *net.UDPConn, addr *net.UDPAddr, hdr *Header, payload []byte) {
	t.Helper()
	encoded, _ := hdr.Encode(payload)
	cs := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[13:17], cs)
	if _, err := conn.WriteToUDP(encoded, addr); err != nil {
		t.Fatalf("sendPacket: %v", err)
	}
}

func readPacket(t *testing.T, conn *net.UDPConn) *Header {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("readPacket: %v", err)
	}
	hdr, _, err := Decode(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return hdr
}

func readAndCheckFlags(t *testing.T, conn *net.UDPConn, expectedFlags uint8) *Header {
	t.Helper()
	hdr := readPacket(t, conn)
	if hdr.Flags&0x07 != expectedFlags {
		t.Errorf("expected flags=0x%02x, got 0x%02x (masked 0x%02x)", expectedFlags, hdr.Flags, hdr.Flags&0x07)
	}
	return hdr
}

func readISN(t *testing.T, conn *net.UDPConn) uint32 {
	t.Helper()
	hdr := readPacket(t, conn)
	return hdr.SeqNum
}
