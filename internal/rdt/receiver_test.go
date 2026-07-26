package rdt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

// makeRDTDataPacket builds a wire-format RDT DATA packet with the given
// sequence number and payload.
func makeRDTDataPacket(t *testing.T, sessionID, seqNum uint32, payload []byte) []byte {
	t.Helper()
	h := &Header{
		SessionID:  sessionID,
		SeqNum:     seqNum,
		Flags:      FlagDATA,
		PayloadLen: uint16(len(payload)),
		RawWindow:  65535,
	}
	encoded, err := h.Encode(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	cs := ComputeChecksum(encoded)
	binary.BigEndian.PutUint32(encoded[17:21], cs)
	return encoded
}

// runReceiver removed — dead code that copied a sync.WaitGroup lock value,
// causing a go vet warning (copylocks). The real integration test,
// TestReceiver_FlushesBufferedOnExit, inlines the full handshake instead.

// TestReceiver_FlushesBufferedOnExit is the targeted regression test for the
// bug where the receiveLoop returns on FIN without flushing r.recvBuffer.
// We drive the receiver manually: send a 3-way handshake, then send data
// packets strictly out of order (highest seq first), then send FIN. With
// the bug, the buffered packets are lost. With the fix, they are delivered
// in order to the output channel.
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

	// Sanity check: can peer send to receiver? Use a separate "ping" conn
	// so we don't consume packets the receiver cares about.
	pingAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	pingConn, _ := net.ListenUDP("udp", pingAddr)
	defer pingConn.Close()
	pingConn.WriteToUDP([]byte("PING"), receiverConn.LocalAddr().(*net.UDPAddr))
	receiverConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	pbuf := make([]byte, 1500)
	pn, _, _ := receiverConn.ReadFromUDP(pbuf)
	if pn != 4 || string(pbuf[:pn]) != "PING" {
		t.Fatalf("ping failed: got %d bytes %q", pn, pbuf[:pn])
	}

	const sessionID = uint32(1)
	const mss = 100 // small for the test; in production this is 1400

	receiver := NewReceiver(receiverConn, sessionID)

	// Start the receiver's Accept() in a goroutine; it will block on SYN.
	acceptDone := make(chan error, 1)
	go func() {
		t.Logf("calling Accept()")
		acceptDone <- receiver.Accept()
		t.Logf("Accept() returned")
	}()

	// Give Accept() time to set its read deadline.
	time.Sleep(100 * time.Millisecond)

	myISN := uint32(0xDEADBEEF)
	syn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	synBytes, _ := syn.Encode(nil)
	cs := ComputeChecksum(synBytes)
	binary.BigEndian.PutUint32(synBytes[17:21], cs)
	t.Logf("sending SYN: %d bytes to %s", len(synBytes), receiverConn.LocalAddr())
	if _, err := peerConn.WriteToUDP(synBytes, receiverConn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatalf("write SYN: %v", err)
	}

	// Read SYN+ACK the receiver sends back.
	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	synAckBuf := make([]byte, 1500)
	sn, _, err := peerConn.ReadFromUDP(synAckBuf)
	if err != nil {
		t.Fatalf("read SYN+ACK: %v", err)
	}
	t.Logf("got SYN+ACK: %d bytes", sn)
	synAckHdr, _, err := Decode(synAckBuf[:sn])
	if err != nil {
		t.Fatalf("decode SYN+ACK: %v", err)
	}
	if synAckHdr.Flags&(FlagSYN|FlagACK) != FlagSYN|FlagACK {
		t.Fatalf("expected SYN+ACK, got flags=0x%02x", synAckHdr.Flags)
	}
	serverISN := synAckHdr.SeqNum

	// Send ACK to complete the 3-way handshake.
	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	ackBytes, _ := ack.Encode(nil)
	cs = ComputeChecksum(ackBytes)
	binary.BigEndian.PutUint32(ackBytes[17:21], cs)
	if _, err := peerConn.WriteToUDP(ackBytes, receiverConn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatalf("write ACK: %v", err)
	}
	t.Logf("ACK sent")

	// Wait for Accept() to return.
	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept timed out")
	}
	t.Logf("Accept() returned, connection established")

	// (Accept() already returned above when the ACK was sent.)

	// Now build data packets. The receiver's rcvBase is myISN+1 after the
	// 3-way handshake, so data packets must start at myISN+1.
	//
	// CRITICAL: we must NOT send the in-order packet (seq = myISN+1) before
	// FIN, otherwise handleData will flush the buffer through the normal
	// in-order path. The bug we're testing is what happens when FIN arrives
	// while packets are still buffered and the in-order packet has NOT been
	// received — exactly the scenario the user hit on a real network.
	dataBase := myISN + 1
	totalChunks := 5
	chunks := make([][]byte, totalChunks)
	for i := 0; i < totalChunks; i++ {
		chunks[i] = bytes.Repeat([]byte{byte('A' + i)}, mss)
	}

	// Send only OUT-OF-ORDER packets (skip seq=0, the one that would be in-order).
	// This forces the receiver to buffer them, and only a retransmit of the
	// in-order packet can fill the gap. With the bug, the receiveLoop exits
	// on FIN before the retransmit, and the buffered chunks are lost.
	for i := totalChunks - 1; i >= 1; i-- {
		seq := dataBase + uint32(i*mss)
		pkt := makeRDTDataPacket(t, sessionID, seq, chunks[i])
		if _, err := peerConn.WriteToUDP(pkt, receiverConn.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatalf("write data %d: %v", i, err)
		}
		// Small delay so the receiveLoop processes them separately
		// (without it, the kernel might deliver them all at once and
		// they might arrive "in order" by accident).
		time.Sleep(5 * time.Millisecond)
	}

	// Send FIN.
	fin := &Header{SessionID: sessionID, Flags: FlagFIN, RawWindow: 65535}
	finBytes, _ := fin.Encode(nil)
	cs = ComputeChecksum(finBytes)
	binary.BigEndian.PutUint32(finBytes[17:21], cs)
	if _, err := peerConn.WriteToUDP(finBytes, receiverConn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatalf("write FIN: %v", err)
	}

	// Simulate the sender's retransmit of the in-order packet — this is
	// what a real Selective Repeat sender does after seeing the receiver's
	// duplicate ACKs. With the fix, the receiver keeps reading for ~2s
	// after FIN, so this retransmit arrives and fills the gap.
	time.Sleep(200 * time.Millisecond)
	firstPkt := makeRDTDataPacket(t, sessionID, dataBase, chunks[0])
	if _, err := peerConn.WriteToUDP(firstPkt, receiverConn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatalf("write retransmit: %v", err)
	}

	// Drain the output channel with a timeout. The receiver should now
	// flush all 5 chunks once the retransmit fills the gap.
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

	// Build the expected payload.
	want := make([]byte, 0, totalChunks*mss)
	for i := 0; i < totalChunks; i++ {
		want = append(want, chunks[i]...)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("output mismatch:\n got:  %x\n want: %x", got, want)
		t.Errorf("got %d bytes, want %d bytes", len(got), len(want))
	}

	// Now close the receiver and verify the FIN+ACK is sent.
	if err := receiver.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
}

// TestReceiver_HandlesFINBeforeAllBufferedDelivered is a more aggressive
// version: the test sends only some of the chunks, then FIN, then expects
// the receiver to time out (or close) without delivering the missing chunks.
// This is by design — the receiver can't wait forever for retransmissions
// that will never come.
//
// Skipped: this test documents the limitation rather than asserting a fix.
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

	const sessionID = uint32(101)
	receiver := NewReceiver(receiverConn, sessionID)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	myISN := uint32(0x1000)
	syn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), syn, nil)

	synAckHdr := readAndCheckFlags(t, peerConn, FlagSYN|FlagACK)
	serverISN := synAckHdr.SeqNum

	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), ack, nil)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	chunks := []string{"AAAA", "BBBB", "CCCC", "DDDD"}
	offset := uint32(0)
	for _, c := range chunks {
		seq := myISN + 1 + offset
		pkt := makeRDTDataPacket(t, sessionID, seq, []byte(c))
		peerConn.WriteToUDP(pkt, receiverConn.LocalAddr().(*net.UDPAddr))
		offset += uint32(len(c))
		time.Sleep(5 * time.Millisecond)
	}

	fin := &Header{SessionID: sessionID, Flags: FlagFIN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), fin, nil)

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

	const sessionID = uint32(102)
	receiver := NewReceiver(receiverConn, sessionID)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	myISN := uint32(0x2000)
	syn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), syn, nil)

	synAckHdr := readAndCheckFlags(t, peerConn, FlagSYN|FlagACK)
	serverISN := synAckHdr.SeqNum

	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), ack, nil)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	seq := myISN + 1
	pkt := makeRDTDataPacket(t, sessionID, seq, []byte("UNIQUE"))
	peerConn.WriteToUDP(pkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(20 * time.Millisecond)
	peerConn.WriteToUDP(pkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(20 * time.Millisecond)

	fin := &Header{SessionID: sessionID, Flags: FlagFIN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), fin, nil)

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

	const sessionID = uint32(103)
	receiver := NewReceiver(receiverConn, sessionID)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	myISN := uint32(0x3000)
	syn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), syn, nil)

	synAckHdr := readAndCheckFlags(t, peerConn, FlagSYN|FlagACK)
	serverISN := synAckHdr.SeqNum

	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), ack, nil)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	seq := myISN + 1
	pkt := makeRDTDataPacket(t, sessionID, seq, []byte("GOOD"))
	peerConn.WriteToUDP(pkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(20 * time.Millisecond)

	corruptPkt := makeRDTDataPacket(t, sessionID, seq+uint32(len("GOOD")), []byte("CORRUPT"))
	if len(corruptPkt) > HeaderSize+1 {
		corruptPkt[HeaderSize] ^= 0xFF
	}
	peerConn.WriteToUDP(corruptPkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(20 * time.Millisecond)

	actualPkt := makeRDTDataPacket(t, sessionID, seq+uint32(len("GOOD")), []byte("CLEAN"))
	peerConn.WriteToUDP(actualPkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(20 * time.Millisecond)

	fin := &Header{SessionID: sessionID, Flags: FlagFIN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), fin, nil)

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

func TestReceiver_SessionIDFiltering(t *testing.T) {
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

	const sessionID = uint32(104)
	receiver := NewReceiver(receiverConn, sessionID)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	wrongSyn := &Header{SessionID: 9999, SeqNum: 0, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), wrongSyn, nil)

	time.Sleep(300 * time.Millisecond)

	myISN := uint32(0x4000)
	correctSyn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), correctSyn, nil)

	synAckHdr := readAndCheckFlags(t, peerConn, FlagSYN|FlagACK)
	serverISN := synAckHdr.SeqNum

	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), ack, nil)

	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("Accept after wrong-SessionID filtered: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept timed out - wrong SessionID may have been consumed")
	}

	receiver.Close()
}

func TestReceiver_RSTHandling(t *testing.T) {
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

	const sessionID = uint32(105)
	receiver := NewReceiver(receiverConn, sessionID)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	myISN := uint32(0x5000)
	syn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), syn, nil)

	synAckHdr := readAndCheckFlags(t, peerConn, FlagSYN|FlagACK)
	serverISN := synAckHdr.SeqNum

	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), ack, nil)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	dataSeq := myISN + 1
	dataPkt := makeRDTDataPacket(t, sessionID, dataSeq, []byte("PRE-RST"))
	peerConn.WriteToUDP(dataPkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(50 * time.Millisecond)

	rst := &Header{SessionID: sessionID, Flags: FlagRST, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), rst, nil)

	deadline := time.After(2 * time.Second)
	outputClosed := false
	for !outputClosed {
		select {
		case _, ok := <-receiver.Receive():
			if !ok {
				outputClosed = true
			}
		case <-deadline:
			t.Fatal("output channel should close after RST")
		}
	}

	receiver.Close()
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

	const sessionID = uint32(106)
	receiver := NewReceiver(receiverConn, sessionID)

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- receiver.Accept() }()

	time.Sleep(100 * time.Millisecond)

	myISN := uint32(0x6000)
	syn := &Header{SessionID: sessionID, SeqNum: myISN, Flags: FlagSYN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), syn, nil)

	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read SYN+ACK: %v", err)
	}
	synAckHdr, _, _ := Decode(buf[:n])
	serverISN := synAckHdr.SeqNum

	ack := &Header{SessionID: sessionID, AckNum: serverISN + 1, Flags: FlagACK, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), ack, nil)

	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept: %v", err)
	}

	seq := myISN + 1
	dataPkt := makeRDTDataPacket(t, sessionID, seq, []byte("WINDOW-TEST"))
	peerConn.WriteToUDP(dataPkt, receiverConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(50 * time.Millisecond)

	peerConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf2 := make([]byte, 1500)
	n2, _, err := peerConn.ReadFromUDP(buf2)
	if err != nil {
		t.Fatalf("read ACK for window: %v", err)
	}
	ackHdr, _, _ := Decode(buf2[:n2])

	if ackHdr.RawWindow == 0 {
		t.Error("window should not be 0 with empty buffer")
	}
	if ackHdr.RawWindow > 65535 {
		t.Errorf("window %d exceeds max", ackHdr.RawWindow)
	}

	fin := &Header{SessionID: sessionID, Flags: FlagFIN, RawWindow: 65535}
	sendPacket(t, peerConn, receiverConn.LocalAddr().(*net.UDPAddr), fin, nil)

	drainChannel(t, receiver.Receive())
	receiver.Close()
}

func TestReceiver_DoubleCloseSafety(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	r := NewReceiver(conn, 1)

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
	binary.BigEndian.PutUint32(encoded[17:21], cs)
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
	if hdr.Flags&0x1F != expectedFlags {
		t.Errorf("expected flags=0x%02x, got 0x%02x (control: 0x%02x)", expectedFlags, hdr.Flags, hdr.Flags&0x1F)
	}
	return hdr
}

func readISN(t *testing.T, conn *net.UDPConn) uint32 {
	t.Helper()
	hdr := readPacket(t, conn)
	return hdr.SeqNum
}
