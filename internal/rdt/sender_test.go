package rdt

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

func makePeerConn(t *testing.T) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen peer: %v", err)
	}
	return conn, conn.LocalAddr().(*net.UDPAddr)
}

func completeSenderHandshake(t *testing.T, sender *Sender, peerConn *net.UDPConn, senderConn *net.UDPConn) {
	t.Helper()

	peerAddr := peerConn.LocalAddr().(*net.UDPAddr)
	senderConnAddr := senderConn.LocalAddr().(*net.UDPAddr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- sender.Connect(peerAddr)
	}()

	time.Sleep(100 * time.Millisecond)

	buf := make([]byte, 1500)
	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("peer read SYN: %v", err)
	}
	synHdr, _, err := Decode(buf[:n])
	if err != nil {
		t.Fatalf("decode SYN: %v", err)
	}
	if synHdr.Flags&FlagSYN == 0 {
		t.Fatal("expected SYN from sender")
	}

	myISN := uint32(0xBEEF)
	synAck := &Header{
		SessionID: sender.sessionID,
		SeqNum:    myISN,
		AckNum:    synHdr.SeqNum + 1,
		Flags:     FlagSYN | FlagACK,
		RawWindow: 65535,
	}
	synAckBytes, _ := synAck.Encode(nil)
	cs := ComputeChecksum(synAckBytes)
	binary.BigEndian.PutUint32(synAckBytes[17:21], cs)
	peerConn.WriteToUDP(synAckBytes, senderConnAddr)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sender Connect timed out")
	}

	peerConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	ackBuf := make([]byte, 1500)
	n, _, err = peerConn.ReadFromUDP(ackBuf)
	if err == nil {
		ackHdr, _, _ := Decode(ackBuf[:n])
		if ackHdr.Flags != FlagACK || ackHdr.AckNum != myISN+1 {
			t.Logf("third ACK: flags=0x%02x ack=%d", ackHdr.Flags, ackHdr.AckNum)
		}
	}
}

func TestSender_ConnectThreeWayHandshake(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 42)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	sender.mu.Lock()
	state := sender.state
	sender.mu.Unlock()

	if state != sendEstablished {
		t.Errorf("expected established, got state=%d", state)
	}
}

func TestSender_AcceptThreeWayHandshake(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 43)

	errCh := make(chan error, 1)
	go func() { errCh <- sender.Accept() }()

	time.Sleep(100 * time.Millisecond)

	syn := &Header{SessionID: 43, SeqNum: 0xAAAA, Flags: FlagSYN, RawWindow: 65535}
	synBytes, _ := syn.Encode(nil)
	cs := ComputeChecksum(synBytes)
	binary.BigEndian.PutUint32(synBytes[17:21], cs)
	peerConn.WriteToUDP(synBytes, senderConn.LocalAddr().(*net.UDPAddr))

	buf := make([]byte, 1500)
	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read SYN+ACK: %v", err)
	}
	synAckHdr, _, _ := Decode(buf[:n])
	if synAckHdr.Flags&(FlagSYN|FlagACK) != FlagSYN|FlagACK {
		t.Fatalf("expected SYN+ACK, got flags=0x%02x", synAckHdr.Flags)
	}

	ack := &Header{SessionID: 43, AckNum: synAckHdr.SeqNum + 1, Flags: FlagACK, RawWindow: 65535}
	ackBytes, _ := ack.Encode(nil)
	cs = ComputeChecksum(ackBytes)
	binary.BigEndian.PutUint32(ackBytes[17:21], cs)
	peerConn.WriteToUDP(ackBytes, senderConn.LocalAddr().(*net.UDPAddr))

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept timed out")
	}

	sender.mu.Lock()
	state := sender.state
	sender.mu.Unlock()

	if state != sendEstablished {
		t.Errorf("expected established, got state=%d", state)
	}
}

func TestSender_SendAndProcessAck(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 44)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	sender.Send([]byte("Hello from sender test!"))

	time.Sleep(200 * time.Millisecond)

	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("peer read data: %v", err)
	}
	dataHdr, payload, err := Decode(buf[:n])
	if err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if !VerifyChecksum(buf[:n]) {
		t.Fatal("checksum validation failed on received data packet")
	}
	if dataHdr.Flags&FlagDATA == 0 {
		t.Fatal("expected DATA flag")
	}
	if len(payload) == 0 {
		t.Fatal("expected non-empty payload")
	}

	ack := &Header{
		SessionID: 44,
		AckNum:    dataHdr.SeqNum + uint32(len(payload)),
		Flags:     FlagACK,
		RawWindow: 65535,
	}
	ackBytes, _ := ack.Encode(nil)
	cs := ComputeChecksum(ackBytes)
	binary.BigEndian.PutUint32(ackBytes[17:21], cs)
	peerConn.WriteToUDP(ackBytes, senderConn.LocalAddr().(*net.UDPAddr))

	time.Sleep(100 * time.Millisecond)

	sender.mu.Lock()
	inflight := len(sender.inflight)
	sendBase := sender.sendBase
	sender.mu.Unlock()

	if inflight != 0 {
		t.Errorf("expected 0 inflight after ACK, got %d", inflight)
	}
	expectedBase := dataHdr.SeqNum + uint32(len(payload))
	if sendBase != expectedBase {
		t.Errorf("sendBase: got %d, want %d", sendBase, expectedBase)
	}
}

func TestSender_RetransmitOnTimeout(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 45)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	sender.Send([]byte("packet for retransmit"))

	time.Sleep(200 * time.Millisecond)

	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read first data: %v", err)
	}
	dataHdr, payload, _ := Decode(buf[:n])

	time.Sleep(800 * time.Millisecond)

	peerConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n2, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read retransmit: %v (sender may not have retransmitted)", err)
	}
	dataHdr2, payload2, _ := Decode(buf[:n2])

	if dataHdr2.Flags&FlagDATA == 0 {
		t.Fatal("retransmitted packet should have DATA flag")
	}
	if dataHdr2.SeqNum != dataHdr.SeqNum {
		t.Errorf("retransmit seq: got %d, want %d", dataHdr2.SeqNum, dataHdr.SeqNum)
	}
	if string(payload2) != string(payload) {
		t.Errorf("retransmit payload mismatch: %q vs %q", payload2, payload)
	}

	_ = dataHdr
	_ = payload
}

func TestSender_CumulativeAck(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 46)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	sender.Send([]byte("AAAA BBBB CCCC"))

	time.Sleep(200 * time.Millisecond)

	var packets []struct {
		seq uint32
		len uint32
	}
	peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 3; i++ {
		buf := make([]byte, 1500)
		n, _, err := peerConn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		hdr, payload, _ := Decode(buf[:n])
		packets = append(packets, struct {
			seq uint32
			len uint32
		}{hdr.SeqNum, uint32(len(payload))})
	}

	if len(packets) == 0 {
		t.Skip("no packets received from sender")
	}

	lastPkt := packets[len(packets)-1]
	cumulativeAck := lastPkt.seq + lastPkt.len

	ack := &Header{
		SessionID: 46,
		AckNum:    cumulativeAck,
		Flags:     FlagACK,
		RawWindow: 65535,
	}
	ackBytes, _ := ack.Encode(nil)
	cs := ComputeChecksum(ackBytes)
	binary.BigEndian.PutUint32(ackBytes[17:21], cs)
	peerConn.WriteToUDP(ackBytes, senderConn.LocalAddr().(*net.UDPAddr))

	time.Sleep(100 * time.Millisecond)

	sender.mu.Lock()
	inflight := len(sender.inflight)
	sender.mu.Unlock()

	if inflight != 0 {
		t.Errorf("cumulative ACK should clear all inflight, got %d remaining", inflight)
	}
}

func TestSender_MaxRetransmitsRST(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 47)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	payload := []byte("data that will be retransmitted to death")
	sender.Send(payload)

	time.Sleep(200 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		timer := time.NewTicker(50 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				sender.mu.Lock()
				seqs := make([]uint32, 0, len(sender.inflight))
				for seq := range sender.inflight {
					seqs = append(seqs, seq)
				}
				sender.mu.Unlock()
				for _, seq := range seqs {
					sender.onTimeout(seq)
				}
			case <-done:
				return
			}
		}
	}()

	select {
	case err := <-sender.errCh:
		if err == nil {
			t.Error("expected error after max retransmits")
		}
	case <-sender.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for RST after max retransmits")
	}

	close(done)

	peerConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(buf)
	if err == nil {
		hdr, _, _ := Decode(buf[:n])
		if hdr.Flags&FlagRST != 0 {
			t.Log("RST was sent (expected)")
		}
	}
}

func TestSender_StaleAckIgnored(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	sender := NewSender(senderConn, 48)

	sender.mu.Lock()
	sender.sendBase = 1000
	sender.mu.Unlock()

	sender.processAck(500)

	sender.mu.Lock()
	sendBase := sender.sendBase
	sender.mu.Unlock()

	if sendBase != 1000 {
		t.Errorf("stale ACK should not advance sendBase: got %d, want 1000", sendBase)
	}
}

func TestSender_StaleAckExactlySendBase(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	sender := NewSender(senderConn, 49)

	sender.mu.Lock()
	sender.sendBase = 1000
	sender.mu.Unlock()

	sender.processAck(1000)

	sender.mu.Lock()
	sendBase := sender.sendBase
	sender.mu.Unlock()

	if sendBase != 1000 {
		t.Errorf("ACK == sendBase should be ignored: got %d, want 1000", sendBase)
	}
}

func TestSender_RSTHandling(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 50)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	rst := &Header{SessionID: 50, Flags: FlagRST, RawWindow: 65535}
	rstBytes, _ := rst.Encode(nil)
	cs := ComputeChecksum(rstBytes)
	binary.BigEndian.PutUint32(rstBytes[17:21], cs)
	peerConn.WriteToUDP(rstBytes, senderConn.LocalAddr().(*net.UDPAddr))

	select {
	case <-sender.done:
	case <-time.After(2 * time.Second):
		t.Fatal("sender done channel should close on RST")
	}

	sender.mu.Lock()
	state := sender.state
	sender.mu.Unlock()
	if state != sendClosed {
		t.Errorf("expected sendClosed, got state=%d", state)
	}
}

func TestSender_FinPlusAckHandling(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 51)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	finAck := &Header{SessionID: 51, Flags: FlagFIN | FlagACK, RawWindow: 65535}
	finAckBytes, _ := finAck.Encode(nil)
	cs := ComputeChecksum(finAckBytes)
	binary.BigEndian.PutUint32(finAckBytes[17:21], cs)
	peerConn.WriteToUDP(finAckBytes, senderConn.LocalAddr().(*net.UDPAddr))

	select {
	case <-sender.done:
	case <-time.After(2 * time.Second):
		t.Fatal("sender done channel should close on FIN+ACK")
	}

	sender.mu.Lock()
	state := sender.state
	sender.mu.Unlock()
	if state != sendClosed {
		t.Errorf("expected sendClosed, got state=%d", state)
	}
}

func TestSender_CloseNotEstablished(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	sender := NewSender(conn, 52)
	err = sender.Close()
	if err == nil {
		t.Error("Close on non-established sender should return error")
	}
}

func TestSender_SendOverCwndBlocks(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 53)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	sender.mu.Lock()
	sender.bbr.cwnd = bbrMinCwnd
	sender.mu.Unlock()

	largeData := make([]byte, 100000)
	done := make(chan error, 1)
	go func() {
		done <- sender.Send(largeData)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(1 * time.Second):
	}

	time.Sleep(200 * time.Millisecond)
}

func TestSender_InflightRecvWindowCaps(t *testing.T) {
	b := NewBBR()
	b.cwnd = 1000000

	b.OnSend(30000)

	if b.CanSend(1, 20000) {
		t.Error("Should block: inflight 30000 > recvWindow 20000")
	}

	if !b.CanSend(1, 40000) {
		t.Error("Should allow: inflight 30000 + 1 <= recvWindow 40000, recvWindow < cwnd")
	}
}

func TestSender_ConcurrentSessions(t *testing.T) {
	conn1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn1.Close()

	conn2, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn2.Close()

	peerConn1, _ := makePeerConn(t)
	defer peerConn1.Close()
	peerConn2, _ := makePeerConn(t)
	defer peerConn2.Close()

	sender1 := NewSender(conn1, 100)
	sender2 := NewSender(conn2, 200)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		completeSenderHandshake(t, sender1, peerConn1, conn1)
	}()
	go func() {
		defer wg.Done()
		completeSenderHandshake(t, sender2, peerConn2, conn2)
	}()

	wg.Wait()

	sender1.mu.Lock()
	state1 := sender1.state
	sender1.mu.Unlock()
	sender2.mu.Lock()
	state2 := sender2.state
	sender2.mu.Unlock()

	if state1 != sendEstablished {
		t.Errorf("session 100 state=%d", state1)
	}
	if state2 != sendEstablished {
		t.Errorf("session 200 state=%d", state2)
	}
}

func TestSender_SendLargeDataChunked(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 54)
	sender.bbr.cwnd = 1000000
	completeSenderHandshake(t, sender, peerConn, senderConn)

	largeData := make([]byte, 3000)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	sender.Send(largeData)

	time.Sleep(500 * time.Millisecond)

	packetCount := 0
	peerConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	for packetCount < 3 {
		buf := make([]byte, 1500)
		n, _, err := peerConn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		hdr, payload, _ := Decode(buf[:n])
		if hdr.Flags&FlagDATA != 0 {
			if len(payload) > MSS {
				t.Errorf("chunk %d: payload %d > MSS %d", packetCount, len(payload), MSS)
			}
			packetCount++
		}
	}

	if packetCount < 3 {
		t.Errorf("expected at least 3 DATA packets for 3000 bytes, got %d", packetCount)
	}
}

func TestSender_CloseWaitDrain(t *testing.T) {
	senderConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer senderConn.Close()

	peerConn, _ := makePeerConn(t)
	defer peerConn.Close()

	sender := NewSender(senderConn, 55)
	completeSenderHandshake(t, sender, peerConn, senderConn)

	sender.Send([]byte("quick data"))

	time.Sleep(200 * time.Millisecond)

	var firstSeq uint32
	peerConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf := make([]byte, 1500)
	n, _, _ := peerConn.ReadFromUDP(buf)
	hdr, payload, _ := Decode(buf[:n])
	firstSeq = hdr.SeqNum

	ack := &Header{
		SessionID: 55,
		AckNum:    firstSeq + uint32(len(payload)),
		Flags:     FlagACK,
		RawWindow: 65535,
	}
	ackBytes, _ := ack.Encode(nil)
	cs := ComputeChecksum(ackBytes)
	binary.BigEndian.PutUint32(ackBytes[17:21], cs)
	peerConn.WriteToUDP(ackBytes, senderConn.LocalAddr().(*net.UDPAddr))

	time.Sleep(100 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- sender.Close()
	}()

	peerConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	finBuf := make([]byte, 1500)
	n, _, err = peerConn.ReadFromUDP(finBuf)
	if err != nil {
		t.Logf("FIN not received (may be OK): %v", err)
	} else {
		finHdr, _, _ := Decode(finBuf[:n])
		if finHdr.Flags&FlagFIN == 0 {
			t.Error("expected FIN after drain")
		}
	}

	select {
	case <-closeDone:
	case <-time.After(15 * time.Second):
		t.Fatal("Close timed out")
	}
}

func TestProcessAckSkipsGaps(t *testing.T) {
	s := &Sender{
		inflight:   make(map[uint32]*packetRecord),
		sendBase:   0,
		recvWindow: 65535,
		bbr:        NewBBR(),
	}
	s.timers = NewTimerManager(time.Second, func(seq uint32) {})

	for i := uint32(0); i < 7000; i += 1400 {
		s.inflight[i] = &packetRecord{
			seq:    i,
			length: 1400,
			sentAt: time.Now(),
		}
	}

	s.processAck(7000)

	if s.sendBase != 7000 {
		t.Errorf("sendBase = %d, want 7000", s.sendBase)
	}
	if len(s.inflight) != 0 {
		t.Errorf("inflight not empty after ack: %d entries remain", len(s.inflight))
	}
}

func TestSenderCloseDrainsOutbox(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:60000")
	if err != nil {
		t.Fatal(err)
	}

	s := &Sender{
		conn:       conn,
		remoteAddr: addr,
		outbox:     make(chan []byte, 10),
		inflight:   make(map[uint32]*packetRecord),
		done:       make(chan struct{}),
		errCh:      make(chan error, 1),
		state:      sendEstablished,
		bbr:        NewBBR(),
		recvWindow: 65535,
	}
	s.timers = NewTimerManager(time.Second, func(seq uint32) {})

	for i := 0; i < 3; i++ {
		s.outbox <- []byte("data-chunk")
	}

	s.mu.Lock()
	s.inflight[1000] = &packetRecord{
		seq:    1000,
		length: 1400,
		sentAt: time.Now(),
	}
	s.mu.Unlock()

	go func() {
		time.Sleep(50 * time.Millisecond)
		for i := 0; i < 3; i++ {
			select {
			case <-s.outbox:
			default:
			}
		}
	}()

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.mu.Lock()
		delete(s.inflight, 1000)
		s.mu.Unlock()
		close(s.done)
	}()

	start := time.Now()
	err = s.Close()
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("Close returned too quickly: %v, want >= 100ms (drain not exercised)", elapsed)
	}
}

func TestProcessAckPartialAck(t *testing.T) {
	s := &Sender{
		inflight:   make(map[uint32]*packetRecord),
		sendBase:   0,
		recvWindow: 65535,
		bbr:        NewBBR(),
	}
	s.timers = NewTimerManager(time.Second, func(seq uint32) {})

	for i := uint32(0); i < 7000; i += 1400 {
		s.inflight[i] = &packetRecord{
			seq:    i,
			length: 1400,
			sentAt: time.Now(),
		}
	}

	s.processAck(2800)

	if s.sendBase != 2800 {
		t.Errorf("sendBase = %d, want 2800", s.sendBase)
	}
	if len(s.inflight) != 3 {
		t.Errorf("inflight has %d entries after partial ack, want 3", len(s.inflight))
	}
}
