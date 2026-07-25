package main

import (
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/tcpctrl"
)

var (
	serverHost = flag.String("host", "127.0.0.1", "Server host")
	ctrlPort   = flag.String("ctrl", "2121", "Control port")
)

func main() {
	flag.Parse()

	addr := net.JoinHostPort(*serverHost, *ctrlPort)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("cannot connect to %s: %v", addr, err)
	}
	defer conn.Close()

	greeting, err := tcpctrl.ReadLine(conn)
	if err != nil {
		log.Fatalf("read greeting: %v", err)
	}
	fmt.Println(greeting)

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("ftp> ")
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			fmt.Print("ftp> ")
			continue
		}

		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "LCD ") {
			path := strings.TrimSpace(line[4:])
			if err := os.Chdir(path); err != nil {
				fmt.Printf("lcd: %v\n", err)
			}
			fmt.Print("ftp> ")
			continue
		}
		if upper == "LLS" || strings.HasPrefix(upper, "LLS ") {
			path := "."
			if len(line) > 4 {
				path = strings.TrimSpace(line[4:])
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				fmt.Printf("lls: %v\n", err)
			} else {
				for _, e := range entries {
					info, _ := e.Info()
					if e.IsDir() {
						fmt.Printf("%s/\n", e.Name())
					} else {
						fmt.Printf("%-20s %8d\n", e.Name(), info.Size())
					}
				}
			}
			fmt.Print("ftp> ")
			continue
		}
		if upper == "LPWD" {
			wd, _ := os.Getwd()
			fmt.Println(wd)
			fmt.Print("ftp> ")
			continue
		}

		cmd, _ := tcpctrl.ParseCommand(line)

		isDataCmd := cmd == tcpctrl.CmdLIST || cmd == tcpctrl.CmdRETR || cmd == tcpctrl.CmdSTOR || cmd == tcpctrl.CmdNLST || cmd == tcpctrl.CmdSTOU || cmd == tcpctrl.CmdAPPE
		if isDataCmd && connDataMode == modeNone {
			fmt.Println("Use PASV or PORT first.")
			fmt.Print("ftp> ")
			continue
		}

		if cmd != tcpctrl.CmdPORT && cmd != tcpctrl.CmdHASH && cmd != tcpctrl.CmdLIST && cmd != tcpctrl.CmdRETR && cmd != tcpctrl.CmdSTOR && cmd != tcpctrl.CmdNLST && cmd != tcpctrl.CmdSTOU && cmd != tcpctrl.CmdAPPE {
			if _, err := io.WriteString(conn, line+"\r\n"); err != nil {
				log.Fatalf("write error: %v", err)
			}
		}

		switch cmd {
		case tcpctrl.CmdPORT:
			handlePORT(conn)

		case tcpctrl.CmdPASV:
			reply, _ := tcpctrl.ReadLine(conn)
			fmt.Println(reply)
			if strings.HasPrefix(reply, "227") {
				ip, port, parseErr := parsePASV(reply)
				if parseErr != nil {
					fmt.Printf("PASV parse error: %v\n", parseErr)
				} else {
					setupPASVConnection(ip, port)
					connDataMode = modePASV
				}
			}

		case tcpctrl.CmdLIST:
			ensurePASV(conn)
			handleList(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdRETR:
			ensurePASV(conn)
			handleRETR(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdSTOR:
			ensurePASV(conn)
			handleSTOR(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdNLST:
			ensurePASV(conn)
			handleNLST(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdSTOU:
			ensurePASV(conn)
			handleSTOU(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdAPPE:
			ensurePASV(conn)
			handleAPPE(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdHASH:
			handleHASH(conn, strings.TrimSpace(line[4:]))

		case tcpctrl.CmdQUIT:
			reply, _ := tcpctrl.ReadLine(conn)
			fmt.Println(reply)
			return

		default:
			reply, err := tcpctrl.ReadLine(conn)
			if err != nil {
				fmt.Printf("read reply error: %v\n", err)
				return
			}
			fmt.Println(reply)
		}

		fmt.Print("ftp> ")
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scanner error: %v", err)
	}
}

type dataMode int

const (
	modeNone dataMode = iota
	modePASV
	modePORT
)

var (
	connDataMode    = modeNone
	portUDPConn     *net.UDPConn
	currentPASVHost string
	currentPASVPort int
)

func parsePASV(reply string) (string, int, error) {
	start := strings.Index(reply, "(")
	end := strings.LastIndex(reply, ")")
	if start == -1 || end == -1 || start >= end {
		return "", 0, fmt.Errorf("invalid PASV reply: %s", reply)
	}
	parts := strings.Split(reply[start+1:end], ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("invalid PASV address: %s", reply)
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return "", 0, fmt.Errorf("invalid PASV number %s: %v", p, err)
		}
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]
	return host, port, nil
}

func setupPASVConnection(host string, port int) {
	currentPASVHost = host
	currentPASVPort = port
}

func ensurePASV(conn net.Conn) {
	if connDataMode == modePORT || connDataMode == modePASV {
		return
	}
	if connDataMode == modeNone {
		return
	}
}

func prepareDataTransfer() (*net.UDPConn, func(), error) {
	switch connDataMode {
	case modePASV:
		if _, err := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort)); err != nil {
			return nil, nil, fmt.Errorf("PASV address: %w", err)
		}
		udpConn, err := net.ListenUDP("udp", nil)
		if err != nil {
			return nil, nil, fmt.Errorf("ListenUDP: %w", err)
		}
		cleanup := func() {
			udpConn.Close()
		}
		return udpConn, cleanup, nil
	case modePORT:
		if portUDPConn == nil {
			return nil, nil, fmt.Errorf("no PORT socket; send PORT first")
		}
		cleanup := func() {
			portUDPConn.Close()
			portUDPConn = nil
		}
		return portUDPConn, cleanup, nil
	default:
		return nil, nil, fmt.Errorf("no data mode; use PASV or PORT first")
	}
}

func handlePORT(conn net.Conn) {
	ip4 := conn.LocalAddr().(*net.TCPAddr).IP.To4()
	if ip4 == nil {
		fmt.Println("PORT: IPv6 not supported")
		return
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		fmt.Printf("PORT: %v\n", err)
		return
	}

	localPort := udpConn.LocalAddr().(*net.UDPAddr).Port
	p1, p2 := localPort/256, localPort%256
	portCmd := fmt.Sprintf("PORT %d,%d,%d,%d,%d,%d",
		ip4[0], ip4[1], ip4[2], ip4[3], p1, p2)

	io.WriteString(conn, portCmd+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)

	if strings.HasPrefix(reply, "200") {
		connDataMode = modePORT
		portUDPConn = udpConn
	} else {
		udpConn.Close()
	}
}

func computeFileHash(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func handleHASH(conn net.Conn, filename string) {
	localHash, err := computeFileHash(filename)
	if err != nil {
		fmt.Printf("HASH: %v\n", err)
		return
	}

	io.WriteString(conn, "HASH "+filename+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)

	if msg := evalHASHReply(localHash, reply); msg != "" {
		fmt.Println(msg)
	}
}

// evalHASHReply returns the message the client should print after the server
// has replied to a HASH command, or "" if the reply is not a 213 (no message).
// Splitting this from handleHASH makes the matching/parsing logic directly
// testable without a net.Conn.
func evalHASHReply(localHash, reply string) string {
	if !strings.HasPrefix(reply, "213 ") {
		return ""
	}
	remoteHash := parseHASHReply(reply)
	if remoteHash == "" {
		return "HASH: could not parse server reply."
	}
	if strings.EqualFold(localHash, remoteHash) {
		return "Hashes match - file integrity verified."
	}
	return fmt.Sprintf("MISMATCH!\n  Local:  %s\n  Remote: %s", localHash, remoteHash)
}

func parseHASHReply(reply string) string {
	const prefix = "SHA-256 0-"
	idx := strings.Index(reply, prefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(prefix)
	end := start
	for end < len(reply) && end-start < 64 {
		c := reply[end]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			break
		}
		end++
	}
	if end-start != 64 {
		return ""
	}
	return reply[start:end]
}

func handleList(conn net.Conn, path string) {
	if path == "" {
		io.WriteString(conn, "LIST\r\n")
	} else {
		io.WriteString(conn, "LIST "+path+"\r\n")
	}
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	r := rdt.NewReceiver(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := r.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect error: %v\n", err)
			return
		}
	case modePORT:
		if err := r.Accept(); err != nil {
			fmt.Printf("RDT accept error: %v\n", err)
			return
		}
	}

	var listing []byte
	for chunk := range r.Receive() {
		listing = append(listing, chunk...)
	}
	r.Close()
	fmt.Print(string(listing))

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleRETR(conn net.Conn, filename string) {
	if filename == "" {
		io.WriteString(conn, "RETR\r\n")
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	io.WriteString(conn, "RETR "+filename+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	receiver := rdt.NewReceiver(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := receiver.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect: %v\n", err)
			return
		}
	case modePORT:
		if err := receiver.Accept(); err != nil {
			fmt.Printf("RDT accept: %v\n", err)
			return
		}
	}

	localFile, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Cannot create local file: %v\n", err)
		receiver.Close()
		return
	}
	defer localFile.Close()

	progress := rdt.NewProgressBar(0)
	for chunk := range receiver.Receive() {
		n, err := localFile.Write(chunk)
		if err != nil {
			fmt.Printf("Write error: %v\n", err)
			break
		}
		progress.Update(n)
	}
	receiver.Close()
	progress.Done()

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleSTOR(conn net.Conn, filename string) {
	if filename == "" {
		io.WriteString(conn, "STOR\r\n")
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	localFile, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Cannot open local file: %v\n", err)
		return
	}
	defer localFile.Close()

	fileInfo, err := localFile.Stat()
	if err != nil {
		fmt.Printf("Cannot stat local file: %v\n", err)
		return
	}

	io.WriteString(conn, "STOR "+filename+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	sender := rdt.NewSender(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := sender.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect: %v\n", err)
			return
		}
	case modePORT:
		if err := sender.Accept(); err != nil {
			fmt.Printf("RDT accept: %v\n", err)
			return
		}
	}

	progress := rdt.NewProgressBar(fileInfo.Size())
	buf := make([]byte, rdt.MSS)
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if sendErr := sender.Send(buf[:n]); sendErr != nil {
				fmt.Printf("Send error: %v\n", sendErr)
				break
			}
			progress.Update(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			break
		}
	}
	// Wait for remaining inflight data to drain before closing
	time.Sleep(200 * time.Millisecond)
	sender.Close()
	progress.Done()

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleNLST(conn net.Conn, path string) {
	if path == "" {
		io.WriteString(conn, "NLST\r\n")
	} else {
		io.WriteString(conn, "NLST "+path+"\r\n")
	}
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	r := rdt.NewReceiver(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := r.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect error: %v\n", err)
			return
		}
	case modePORT:
		if err := r.Accept(); err != nil {
			fmt.Printf("RDT accept error: %v\n", err)
			return
		}
	}

	var listing []byte
	for chunk := range r.Receive() {
		listing = append(listing, chunk...)
	}
	r.Close()
	fmt.Print(string(listing))

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleSTOU(conn net.Conn, filename string) {
	if filename == "" {
		fmt.Println("STOU requires a local filename to upload.")
		return
	}

	localFile, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Cannot open local file: %v\n", err)
		return
	}
	defer localFile.Close()

	fileInfo, err := localFile.Stat()
	if err != nil {
		fmt.Printf("Cannot stat local file: %v\n", err)
		return
	}

	io.WriteString(conn, "STOU "+filename+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	sender := rdt.NewSender(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := sender.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect: %v\n", err)
			return
		}
	case modePORT:
		if err := sender.Accept(); err != nil {
			fmt.Printf("RDT accept: %v\n", err)
			return
		}
	}

	progress := rdt.NewProgressBar(fileInfo.Size())
	buf := make([]byte, rdt.MSS)
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if sendErr := sender.Send(buf[:n]); sendErr != nil {
				fmt.Printf("Send error: %v\n", sendErr)
				break
			}
			progress.Update(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	sender.Close()
	progress.Done()

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleAPPE(conn net.Conn, filename string) {
	if filename == "" {
		io.WriteString(conn, "APPE\r\n")
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	localFile, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Cannot open local file: %v\n", err)
		return
	}
	defer localFile.Close()

	fileInfo, err := localFile.Stat()
	if err != nil {
		fmt.Printf("Cannot stat local file: %v\n", err)
		return
	}

	io.WriteString(conn, "APPE "+filename+"\r\n")
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpConn, cleanup, err := prepareDataTransfer()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cleanup()

	sessionID := uint32(1)
	sender := rdt.NewSender(udpConn, sessionID)

	switch connDataMode {
	case modePASV:
		udpAddr, _ := net.ResolveUDPAddr("udp",
			fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		if err := sender.Connect(udpAddr); err != nil {
			fmt.Printf("RDT connect: %v\n", err)
			return
		}
	case modePORT:
		if err := sender.Accept(); err != nil {
			fmt.Printf("RDT accept: %v\n", err)
			return
		}
	}

	progress := rdt.NewProgressBar(fileInfo.Size())
	buf := make([]byte, rdt.MSS)
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if sendErr := sender.Send(buf[:n]); sendErr != nil {
				fmt.Printf("Send error: %v\n", sendErr)
				break
			}
			progress.Update(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Read error: %v\n", err)
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	sender.Close()
	progress.Done()

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}
