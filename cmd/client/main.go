package main

import (
	"bufio"
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

		if _, err := io.WriteString(conn, line+"\r\n"); err != nil {
			log.Fatalf("write error: %v", err)
		}

		cmd, _ := tcpctrl.ParseCommand(line)

		switch cmd {
		case tcpctrl.CmdPASV:
			reply, _ := tcpctrl.ReadLine(conn)
			fmt.Println(reply)
			if strings.HasPrefix(reply, "227") {
				ip, port, parseErr := parsePASV(reply)
				if parseErr != nil {
					fmt.Printf("PASV parse error: %v\n", parseErr)
				} else {
					setupPASVConnection(ip, port)
				}
			}

		case tcpctrl.CmdLIST:
			handleList(conn)

		case tcpctrl.CmdRETR:
			handleRETR(conn, strings.TrimSpace(line[5:]))

		case tcpctrl.CmdSTOR:
			handleSTOR(conn, strings.TrimSpace(line[5:]))

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

var (
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

func handleList(conn net.Conn) {
	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	sessionID := uint32(1)
	r := rdt.NewReceiver(udpConn, sessionID)
	if err := r.Connect(udpAddr); err != nil {
		fmt.Printf("RDT connect error: %v\n", err)
		return
	}

	var listing []byte
	for chunk := range r.Receive() {
		listing = append(listing, chunk...)
	}
	fmt.Print(string(listing))

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleRETR(conn net.Conn, filename string) {
	if filename == "" {
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	sessionID := uint32(1)
	receiver := rdt.NewReceiver(udpConn, sessionID)
	if err := receiver.Connect(udpAddr); err != nil {
		fmt.Printf("RDT connect error: %v\n", err)
		return
	}

	localFile, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Cannot create local file: %v\n", err)
		receiver.Close()
		return
	}
	defer localFile.Close()

	total := 0
	for chunk := range receiver.Receive() {
		n, _ := localFile.Write(chunk)
		total += n
	}

	fmt.Printf("Downloaded %d bytes to %s\n", total, filename)

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}

func handleSTOR(conn net.Conn, filename string) {
	if filename == "" {
		reply, _ := tcpctrl.ReadLine(conn)
		if reply != "" {
			fmt.Println(reply)
		}
		return
	}

	reply, _ := tcpctrl.ReadLine(conn)
	fmt.Println(reply)
	if !strings.HasPrefix(reply, "150") {
		return
	}

	localFile, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Cannot open local file: %v\n", err)
		udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
		tmpConn, _ := net.DialUDP("udp", nil, udpAddr)
		tmpConn.Close()
		tcpctrl.ReadLine(conn)
		return
	}
	defer localFile.Close()

	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", currentPASVHost, currentPASVPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	sessionID := uint32(1)
	sender := rdt.NewSender(udpConn, sessionID)
	if err := sender.Connect(udpAddr); err != nil {
		fmt.Printf("RDT connect error: %v\n", err)
		return
	}

	buf := make([]byte, 1400)
	total := 0
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if sendErr := sender.Send(buf[:n]); sendErr != nil {
				fmt.Printf("Send error: %v\n", sendErr)
				break
			}
			total += n
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

	fmt.Printf("Uploaded %d bytes from %s\n", total, filename)

	reply, _ = tcpctrl.ReadLine(conn)
	fmt.Println(reply)
}
