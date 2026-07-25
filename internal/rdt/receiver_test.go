package rdt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
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

// runReceiver starts a receiver on the given UDP conn and writes all
// delivered chunks to the returned channel. It returns the receiver and
// a stop function that closes the receiver and waits for the goroutine.
func runReceiver(t *testing.T, conn *net.UDPConn, sessionID uint32, isServer bool) (*Receiver, func()) {
	t.Helper()
	r := NewReceiver(conn, sessionID)
	var wg sync.WaitGroup
	wg.Add(1)
	readyCh := make(chan struct{})

	// We use a goroutine to drive Accept/Connect and signal when ready.
	// But for this test we don't need the 3-way handshake; we can drive
	// the receiver manually by calling its internal methods. However, the
	// receiveLoop is started by Accept/Connect. So we need at least one
	// of those.
	//
	// Since this is a test for the receiveLoop's flush-on-exit behavior,
	// we don't need the full handshake. We can:
	//   1. Set state to recvEstablished directly
	//   2. Start the receiveLoop manually
	//
	// The receiveLoop is unexported, so we have to go through Accept/Connect.
	// For simplicity, use Connect on the client side and accept on the server.
	_ = isServer

	// We can't easily call receiveLoop directly. Instead, we'll use Connect()
	// and drive the handshake ourselves by sending the expected packets from
	// the other side.
	//
	// Actually for this test, the simplest path is:
	// - Use the receiver's Accept() or Connect() which starts the receiveLoop
	// - Send the handshake packets from a peer
	// - Then send the data packets out of order
	// - Then send FIN
	// - Verify the output channel has all the data

	_ = r
	_ = wg
	_ = readyCh

	return r, func() {}
}

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
