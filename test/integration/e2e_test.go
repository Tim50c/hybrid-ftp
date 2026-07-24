package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"hybrid-ftp/internal/rdt"
)

var (
	serverBin string
	clientBin string
	rootDir   string
	usersFile string
	ctrlPort  string
)

func TestMain(m *testing.M) {
	binSuffix := ""
	if runtime.GOOS == "windows" {
		binSuffix = ".exe"
	}
	serverBin = filepath.Join(os.TempDir(), "hybrid-ftp-server-test"+binSuffix)
	clientBin = filepath.Join(os.TempDir(), "hybrid-ftp-client-test"+binSuffix)

	if err := exec.Command("go", "build", "-o", serverBin, "../../cmd/server").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build server: %v\n", err)
		os.Exit(1)
	}
	if err := exec.Command("go", "build", "-o", clientBin, "../../cmd/client").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build client: %v\n", err)
		os.Exit(1)
	}

	rootDir = filepath.Join(os.TempDir(), "hybrid-ftp-test-root")
	os.MkdirAll(rootDir, 0755)

	usersFile = filepath.Join(os.TempDir(), "hybrid-ftp-test-users.conf")
	os.WriteFile(usersFile, []byte(`admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8ee
`), 0644)

	ctrlPort = "22121"
	code := m.Run()

	os.Remove(serverBin)
	os.Remove(clientBin)
	os.RemoveAll(rootDir)
	os.Remove(usersFile)

	os.Exit(code)
}

func startServer(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	cmd := exec.Command(serverBin,
		"-ctrl", ":"+ctrlPort,
		"-root", rootDir,
		"-users", usersFile,
	)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	return cmd, func() {
		cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}
}

type ftpConn struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

func newFTPConn(t *testing.T, conn net.Conn) *ftpConn {
	t.Helper()
	fc := &ftpConn{conn: conn}
	fc.scanner = bufio.NewScanner(conn)
	fc.scanner.Split(bufio.ScanLines)
	return fc
}

func (fc *ftpConn) sendAndRead(t *testing.T, line string) string {
	t.Helper()
	_, err := io.WriteString(fc.conn, line+"\r\n")
	if err != nil {
		t.Fatalf("send %s: %v", line, err)
	}
	if !fc.scanner.Scan() {
		t.Fatalf("read reply after %s: %v", line, fc.scanner.Err())
	}
	return fc.scanner.Text()
}

func (fc *ftpConn) readReply(t *testing.T) string {
	t.Helper()
	if !fc.scanner.Scan() {
		t.Fatalf("read reply: %v", fc.scanner.Err())
	}
	return fc.scanner.Text()
}

func TestE2E_LoginAndPWD(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", ":"+ctrlPort)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fc := newFTPConn(t, conn)

	greeting := fc.sendAndRead(t, "")
	if !strings.HasPrefix(greeting, "220") {
		t.Errorf("expected 220 greeting, got %s", greeting)
	}

	reply := fc.sendAndRead(t, "USER admin")
	if !strings.HasPrefix(reply, "331") {
		t.Errorf("expected 331, got %s", reply)
	}

	reply = fc.sendAndRead(t, "PASS admin")
	if !strings.HasPrefix(reply, "230") {
		t.Errorf("expected 230, got %s", reply)
	}

	reply = fc.sendAndRead(t, "PWD")
	if !strings.HasPrefix(reply, "257") {
		t.Errorf("expected 257, got %s", reply)
	}

	reply = fc.sendAndRead(t, "QUIT")
	if !strings.HasPrefix(reply, "221") {
		t.Errorf("expected 221, got %s", reply)
	}
}

func TestE2E_AuthFail(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	reply := fc.sendAndRead(t, "PASS wrongpass")
	if !strings.HasPrefix(reply, "530") {
		t.Errorf("expected 530, got %s", reply)
	}
}

func TestE2E_UploadDownload(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	reply := fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("expected 227, got %s", reply)
	}
	pasvHost, pasvPort, err := parsePASVReply(reply)
	if err != nil {
		t.Fatalf("parse PASV: %v", err)
	}

	sampleFile := filepath.Join(rootDir, "uploaded.txt")
	sampleContent := []byte("Hello from integration test!\nThis is a test file.\n")

	if err := os.WriteFile(sampleFile, sampleContent, 0644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	reply = fc.sendAndRead(t, "RETR uploaded.txt")
	if !strings.HasPrefix(reply, "150") {
		t.Fatalf("expected 150, got %s", reply)
	}

	downloaded, err := doRDTDownload(t, pasvHost, pasvPort)
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	if !bytes.Equal(downloaded, sampleContent) {
		t.Errorf("downloaded content mismatch: got %d bytes, want %d bytes", len(downloaded), len(sampleContent))
		t.Logf("got: %q", string(downloaded))
		t.Logf("want: %q", string(sampleContent))
	}

	time.Sleep(500 * time.Millisecond)
	reply = fc.readReply(t)
	if !strings.HasPrefix(reply, "226") {
		t.Errorf("expected 226, got %s", reply)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_ManualInteractiveFlow(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	if r := fc.sendAndRead(t, ""); !strings.HasPrefix(r, "220") {
		t.Fatalf("expected 220, got %s", r)
	}
	if r := fc.sendAndRead(t, "USER admin"); !strings.HasPrefix(r, "331") {
		t.Fatalf("expected 331, got %s", r)
	}
	if r := fc.sendAndRead(t, "PASS admin"); !strings.HasPrefix(r, "230") {
		t.Fatalf("expected 230, got %s", r)
	}

	reply := fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("expected 227, got %s", reply)
	}
	host, port, err := parsePASVReply(reply)
	if err != nil {
		t.Fatalf("parse PASV: %v", err)
	}

	os.MkdirAll(filepath.Join(rootDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(rootDir, "hello.txt"), []byte("hello world\n"), 0644)

	reply = fc.sendAndRead(t, "LIST")
	if !strings.HasPrefix(reply, "150") {
		t.Fatalf("expected 150, got %s", reply)
	}
	listing := doRDTDownloadString(t, host, port)
	if !strings.Contains(listing, "hello.txt") {
		t.Errorf("LIST should contain hello.txt, got: %s", listing)
	}
	r := fc.readReply(t)
	if !strings.HasPrefix(r, "226") {
		t.Errorf("expected 226, got %s", r)
	}

	reply = fc.sendAndRead(t, "CWD subdir")
	if !strings.HasPrefix(reply, "250") {
		t.Errorf("expected 250, got %s", reply)
	}

	r = fc.sendAndRead(t, "PWD")
	if !strings.HasPrefix(r, "257") {
		t.Errorf("expected 257, got %s", r)
	}

	reply = fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("expected 227, got %s", reply)
	}
	host2, port2, err2 := parsePASVReply(reply)
	if err2 != nil {
		t.Fatalf("parse PASV: %v", err2)
	}

	os.WriteFile(filepath.Join(rootDir, "subdir", "nested.txt"), []byte("in subdir\n"), 0644)

	reply = fc.sendAndRead(t, "LIST")
	if !strings.HasPrefix(reply, "150") {
		t.Fatalf("expected 150, got %s", reply)
	}
	listing2 := doRDTDownloadString(t, host2, port2)
	if !strings.Contains(listing2, "nested.txt") {
		t.Errorf("LIST in subdir should contain nested.txt, got: %s", listing2)
	}
	fc.readReply(t)

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_List(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "a.txt"), []byte("aaa\n"), 0644)
	os.WriteFile(filepath.Join(rootDir, "b.txt"), []byte("bbb\n"), 0644)
	os.MkdirAll(filepath.Join(rootDir, "somedir"), 0755)

	reply := fc.sendAndRead(t, "PASV")
	host, port, err := parsePASVReply(reply)
	if err != nil {
		t.Fatalf("parse PASV: %v", err)
	}

	reply = fc.sendAndRead(t, "LIST")
	if !strings.HasPrefix(reply, "150") {
		t.Fatalf("expected 150, got %s", reply)
	}

	listing := doRDTDownloadString(t, host, port)
	if !strings.Contains(listing, "a.txt") {
		t.Errorf("listing should contain a.txt, got: %s", listing)
	}
	if !strings.Contains(listing, "somedir") {
		t.Errorf("listing should contain somedir, got: %s", listing)
	}

	fc.readReply(t)
	fc.sendAndRead(t, "QUIT")
}

func TestE2E_PATH_Containment(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	fc.sendAndRead(t, "PASV")
	reply := fc.sendAndRead(t, "RETR /etc/passwd")
	if !strings.HasPrefix(reply, "550") {
		t.Errorf("RETR /etc/passwd should be 550 (jailed inside root, not found), got %s", reply)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_CWD(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.MkdirAll(filepath.Join(rootDir, "mydir"), 0755)

	reply := fc.sendAndRead(t, "CWD mydir")
	if !strings.HasPrefix(reply, "250") {
		t.Errorf("expected 250, got %s", reply)
	}

	reply = fc.sendAndRead(t, "PWD")
	if !strings.HasPrefix(reply, "257") {
		t.Errorf("expected 257, got %s", reply)
	}
	if !strings.Contains(reply, "mydir") {
		t.Errorf("PWD should show mydir, got %s", reply)
	}

	reply = fc.sendAndRead(t, "CWD /nonexistent")
	if !strings.HasPrefix(reply, "550") {
		t.Errorf("expected 550 for nonexistent dir, got %s", reply)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_MKD_RMD(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "MKD testdir"); !strings.HasPrefix(r, "257") {
		t.Errorf("MKD: expected 257, got %s", r)
	}

	reply := fc.sendAndRead(t, "PASV")
	host, port, _ := parsePASVReply(reply)
	r := fc.sendAndRead(t, "LIST")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("LIST: expected 150, got %s", r)
	}
	listing := doRDTDownloadString(t, host, port)
	if !strings.Contains(listing, "testdir") {
		t.Errorf("LIST should contain testdir, got: %s", listing)
	}
	fc.readReply(t)

	if r := fc.sendAndRead(t, "RMD testdir"); !strings.HasPrefix(r, "250") {
		t.Errorf("RMD: expected 250, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_DELE(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "todelete.txt"), []byte("bye"), 0644)

	if r := fc.sendAndRead(t, "DELE todelete.txt"); !strings.HasPrefix(r, "250") {
		t.Errorf("DELE: expected 250, got %s", r)
	}

	if r := fc.sendAndRead(t, "RETR todelete.txt"); !strings.HasPrefix(r, "550") {
		t.Errorf("RETR deleted file: expected 550, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_CDUP(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.MkdirAll(filepath.Join(rootDir, "cdupdir"), 0755)

	fc.sendAndRead(t, "CWD cdupdir")
	if r := fc.sendAndRead(t, "CDUP"); !strings.HasPrefix(r, "250") {
		t.Errorf("CDUP: expected 250, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_NLST(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "alpha.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(rootDir, "beta.txt"), []byte("b"), 0644)

	reply := fc.sendAndRead(t, "PASV")
	host, port, _ := parsePASVReply(reply)

	r := fc.sendAndRead(t, "NLST")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("NLST: expected 150, got %s", r)
	}
	listing := doRDTDownloadString(t, host, port)
	if !strings.Contains(listing, "alpha.txt") || !strings.Contains(listing, "beta.txt") {
		t.Errorf("NLST missing files, got: %s", listing)
	}
	fc.readReply(t)
	fc.sendAndRead(t, "QUIT")
}

func TestE2E_SIZE_MDTM(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "sized.txt"), []byte("12345"), 0644)

	if r := fc.sendAndRead(t, "SIZE sized.txt"); !strings.HasPrefix(r, "213") || !strings.Contains(r, "5") {
		t.Errorf("SIZE: expected 213 with 5, got %s", r)
	}

	if r := fc.sendAndRead(t, "MDTM sized.txt"); !strings.HasPrefix(r, "213") {
		t.Errorf("MDTM: expected 213, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_RNFR_RNTO(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "original.txt"), []byte("data"), 0644)

	if r := fc.sendAndRead(t, "RNFR original.txt"); !strings.HasPrefix(r, "350") {
		t.Fatalf("RNFR: expected 350, got %s", r)
	}
	if r := fc.sendAndRead(t, "RNTO renamed.txt"); !strings.HasPrefix(r, "250") {
		t.Errorf("RNTO: expected 250, got %s", r)
	}
	if r := fc.sendAndRead(t, "RETR original.txt"); !strings.HasPrefix(r, "550") {
		t.Errorf("RETR old name: expected 550, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_STOU(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	reply := fc.sendAndRead(t, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("PASV: expected 227, got %s", reply)
	}
	host, port, _ := parsePASVReply(reply)

	r := fc.sendAndRead(t, "STOU")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("STOU: expected 150, got %s", r)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	defer udpConn.Close()

	sender := rdt.NewSender(udpConn, 1)
	sender.Connect(udpAddr)
	sender.Send([]byte("unique upload"))
	sender.Close()

	time.Sleep(500 * time.Millisecond)
	r2 := fc.readReply(t)
	if !strings.HasPrefix(r2, "257") {
		t.Errorf("STOU completion: expected 257, got %s", r2)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_APPE(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "append.txt"), []byte("hello"), 0644)

	reply := fc.sendAndRead(t, "PASV")
	host, port, _ := parsePASVReply(reply)

	r := fc.sendAndRead(t, "APPE append.txt")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("APPE: expected 150, got %s", r)
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	defer udpConn.Close()

	sender := rdt.NewSender(udpConn, 1)
	sender.Connect(udpAddr)
	sender.Send([]byte(" world"))
	sender.Close()

	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)

	data, _ := os.ReadFile(filepath.Join(rootDir, "append.txt"))
	if string(data) != "hello world" {
		t.Errorf("append result = %q, want 'hello world'", string(data))
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_HASH(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "hash.txt"), []byte("hello\n"), 0644)

	r := fc.sendAndRead(t, "HASH hash.txt")
	if !strings.HasPrefix(r, "213") {
		t.Errorf("HASH: expected 213, got %s", r)
	}
	if !strings.Contains(r, "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03") {
		t.Errorf("HASH: wrong hash in %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_MODE(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "MODE S"); !strings.HasPrefix(r, "200") {
		t.Errorf("MODE S: expected 200, got %s", r)
	}
	if r := fc.sendAndRead(t, "MODE B"); !strings.HasPrefix(r, "504") {
		t.Errorf("MODE B: expected 504, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_NOOP_HELP(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "NOOP"); !strings.HasPrefix(r, "200") {
		t.Errorf("NOOP: expected 200, got %s", r)
	}
	if r := fc.sendAndRead(t, "HELP"); !strings.HasPrefix(r, "214") {
		t.Errorf("HELP: expected 214, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_PORT_Transfer(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	os.WriteFile(filepath.Join(rootDir, "portfile.txt"), []byte("port mode data\n"), 0644)

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	localPort := udpConn.LocalAddr().(*net.UDPAddr).Port
	p1 := localPort / 256
	p2 := localPort % 256
	portCmd := fmt.Sprintf("PORT 127,0,0,1,%d,%d", p1, p2)

	if r := fc.sendAndRead(t, portCmd); !strings.HasPrefix(r, "200") {
		t.Fatalf("PORT: expected 200, got %s", r)
	}

	r := fc.sendAndRead(t, "RETR portfile.txt")
	if !strings.HasPrefix(r, "150") {
		t.Fatalf("RETR: expected 150, got %s", r)
	}

	sessionID := uint32(1)
	receiver := rdt.NewReceiver(udpConn, sessionID)

	recvCh := make(chan []byte, 1)
	go func() {
		receiver.Accept()
		var buf []byte
		for chunk := range receiver.Receive() {
			buf = append(buf, chunk...)
		}
		recvCh <- buf
	}()

	select {
	case data := <-recvCh:
		if !bytes.Equal(data, []byte("port mode data\n")) {
			t.Errorf("PORT download: got %q", string(data))
		}
	case <-time.After(5 * time.Second):
		receiver.Close()
		t.Error("PORT download timed out")
	}

	time.Sleep(500 * time.Millisecond)
	fc.readReply(t)
	fc.sendAndRead(t, "QUIT")
}

func TestE2E_ConcurrentSessions(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	os.WriteFile(filepath.Join(rootDir, "shared.txt"), []byte("shared content\n"), 0644)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", ":"+ctrlPort)
			if err != nil {
				t.Errorf("session %d dial: %v", id, err)
				return
			}
			defer conn.Close()
			fc := newFTPConn(t, conn)

			fc.sendAndRead(t, "")
			fc.sendAndRead(t, "USER admin")
			fc.sendAndRead(t, "PASS admin")

			reply := fc.sendAndRead(t, "PASV")
			if !strings.HasPrefix(reply, "227") {
				t.Errorf("session %d PASV: expected 227, got %s", id, reply)
				return
			}
			host, port, _ := parsePASVReply(reply)

			r := fc.sendAndRead(t, "RETR shared.txt")
			if !strings.HasPrefix(r, "150") {
				t.Errorf("session %d RETR: expected 150, got %s", id, r)
				return
			}

			data, err := doRDTDownload(t, host, port)
			if err != nil {
				t.Errorf("session %d download: %v", id, err)
				return
			}
			if !bytes.Equal(data, []byte("shared content\n")) {
				t.Errorf("session %d: content mismatch", id)
			}

			time.Sleep(500 * time.Millisecond)
			fc.readReply(t)
			fc.sendAndRead(t, "QUIT")
		}(i)
	}
	wg.Wait()
}

func TestE2E_STAT(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "STAT"); !strings.HasPrefix(r, "211") {
		t.Errorf("STAT (no args): expected 211, got %s", r)
	}

	os.WriteFile(filepath.Join(rootDir, "statfile.txt"), []byte("stat me"), 0644)
	if r := fc.sendAndRead(t, "STAT statfile.txt"); !strings.HasPrefix(r, "213") {
		t.Errorf("STAT (file): expected 213, got %s", r)
	}

	os.MkdirAll(filepath.Join(rootDir, "statdir"), 0755)
	if r := fc.sendAndRead(t, "STAT statdir"); !strings.HasPrefix(r, "212") {
		t.Errorf("STAT (dir): expected 212, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func TestE2E_ABOR(t *testing.T) {
	_, cleanup := startServer(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", ":"+ctrlPort)
	defer conn.Close()
	fc := newFTPConn(t, conn)

	fc.sendAndRead(t, "")
	fc.sendAndRead(t, "USER admin")
	fc.sendAndRead(t, "PASS admin")

	if r := fc.sendAndRead(t, "ABOR"); !strings.HasPrefix(r, "226") {
		t.Errorf("ABOR (no transfer): expected 226, got %s", r)
	}

	fc.sendAndRead(t, "QUIT")
}

func doRDTDownloadString(t *testing.T, host string, port int) string {
	data, err := doRDTDownload(t, host, port)
	if err != nil {
		t.Fatalf("rdt download: %v", err)
	}
	return string(data)
}

func parsePASVReply(reply string) (string, int, error) {
	start := strings.Index(reply, "(")
	end := strings.LastIndex(reply, ")")
	if start == -1 || end == -1 {
		return "", 0, fmt.Errorf("no parentheses in %s", reply)
	}
	parts := strings.Split(reply[start+1:end], ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("expected 6 components, got %d", len(parts))
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n := 0
		fmt.Sscanf(p, "%d", &n)
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]
	return host, port, nil
}

func doRDTDownload(t *testing.T, host string, port int) ([]byte, error) {
	t.Helper()
	addr := fmt.Sprintf("%s:%d", host, port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("listen UDP: %w", err)
	}
	defer udpConn.Close()

	sessionID := uint32(1)
	r := rdt.NewReceiver(udpConn, sessionID)
	if err := r.Connect(udpAddr); err != nil {
		return nil, fmt.Errorf("rdt connect: %w", err)
	}

	recvCh := r.Receive()
	resultCh := make(chan []byte, 1)

	go func() {
		var buf []byte
		for chunk := range recvCh {
			buf = append(buf, chunk...)
		}
		resultCh <- buf
	}()

	select {
	case data := <-resultCh:
		return data, nil
	case <-time.After(4 * time.Second):
		r.Close()
		data := <-resultCh
		return data, nil
	}
}
