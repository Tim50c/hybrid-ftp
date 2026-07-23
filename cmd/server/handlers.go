package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hybrid-ftp/internal/auth"
	"hybrid-ftp/internal/fs"
	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/session"
	"hybrid-ftp/internal/tcpctrl"
)

func setupDataSender(sess *session.Session) (*rdt.Sender, error) {
	if sess.DataMode == "PORT" {
		udpConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		if err != nil {
			return nil, fmt.Errorf("bind udp for PORT: %w", err)
		}
		if sess.DataConn != nil {
			sess.DataConn.Close()
		}
		sess.DataConn = udpConn
		sender := rdt.NewSender(udpConn, sess.ID)
		if err := sender.Connect(sess.DataAddr); err != nil {
			return nil, fmt.Errorf("rdt connect: %w", err)
		}
		return sender, nil
	}
	if sess.DataConn == nil {
		return nil, fmt.Errorf("use PASV or PORT first")
	}
	sender := rdt.NewSender(sess.DataConn, sess.ID)
	if err := sender.Accept(); err != nil {
		return nil, fmt.Errorf("rdt accept: %w", err)
	}
	return sender, nil
}

func setupDataReceiver(sess *session.Session) (*rdt.Receiver, error) {
	if sess.DataMode == "PORT" {
		udpConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		if err != nil {
			return nil, fmt.Errorf("bind udp for PORT: %w", err)
		}
		if sess.DataConn != nil {
			sess.DataConn.Close()
		}
		sess.DataConn = udpConn
		receiver := rdt.NewReceiver(udpConn, sess.ID)
		if err := receiver.Connect(sess.DataAddr); err != nil {
			return nil, fmt.Errorf("rdt connect: %w", err)
		}
		return receiver, nil
	}
	if sess.DataConn == nil {
		return nil, fmt.Errorf("use PASV or PORT first")
	}
	receiver := rdt.NewReceiver(sess.DataConn, sess.ID)
	if err := receiver.Accept(); err != nil {
		return nil, fmt.Errorf("rdt accept: %w", err)
	}
	return receiver, nil
}

func cleanupDataConn(sess *session.Session) {
	if sess.DataConn != nil {
		sess.DataConn.Close()
		sess.DataConn = nil
	}
	sess.DataMode = ""
}

func handleUSER(conn net.Conn, sess *session.Session, usersPath string, args string) {
	sess.Username = args
	tcpctrl.WriteReply(conn, tcpctrl.ReplyNeedPassword, "Password required.")
}

func handlePASS(conn net.Conn, sess *session.Session, usersPath string, args string) {
	if sess.Authenticated {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Already logged in.")
		return
	}
	if auth.CheckPassword(usersPath, sess.Username, args) {
		sess.Authenticated = true
		tcpctrl.WriteReply(conn, tcpctrl.ReplyLoggedIn, "Login successful.")
	} else {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyNotLoggedIn, "Login failed.")
	}
}

func handleQUIT(conn net.Conn, sess *session.Session) bool {
	tcpctrl.WriteReply(conn, tcpctrl.ReplyQuit, "Goodbye.")
	return true
}

func handlePWD(conn net.Conn, sess *session.Session, root string) {
	absRoot, _ := filepath.Abs(root)
	cwd := filepath.Join(absRoot, sess.CWD)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" is current directory.", cwd))
}

func handleCWD(conn net.Conn, sess *session.Session, root string, args string) {
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil || !info.IsDir() {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Directory not found.")
		return
	}
	sess.CWD = fs.ResolvePath(sess, args)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory changed.")
}

func handleTYPE(conn net.Conn, sess *session.Session, args string) {
	if len(args) > 0 && (args[0] == 'A' || args[0] == 'a') {
		sess.TransferType = 'A'
	} else if len(args) > 0 && (args[0] == 'I' || args[0] == 'i') {
		sess.TransferType = 'I'
	} else {
		tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "TYPE must be A or I.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "Type set.")
}

func handlePASV(conn net.Conn, sess *session.Session, args string) {
	udpAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "Cannot bind UDP.")
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "Cannot bind UDP.")
		return
	}
	if sess.DataConn != nil {
		sess.DataConn.Close()
	}
	sess.DataConn = udpConn
	sess.DataMode = "PASV"

	port := udpConn.LocalAddr().(*net.UDPAddr).Port
	host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
	ip := net.ParseIP(host)
	if ip == nil {
		ip = net.ParseIP("127.0.0.1")
	}
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = net.ParseIP("127.0.0.1").To4()
	}
	p1 := port / 256
	p2 := port % 256
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPASV,
		fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d).",
			ip4[0], ip4[1], ip4[2], ip4[3], p1, p2))
}

func handlePORT(conn net.Conn, sess *session.Session, root string, args string) {
	parts := strings.Split(args, ",")
	if len(parts) != 6 {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "PORT requires h1,h2,h3,h4,p1,p2.")
		return
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "Invalid PORT parameter.")
			return
		}
		nums[i] = n
	}
	ip := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]

	sess.DataAddr = &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
	sess.DataMode = "PORT"
	if sess.DataConn != nil {
		sess.DataConn.Close()
		sess.DataConn = nil
	}
	log.Printf("[CTRL] session %d PORT %s:%d", sess.ID, ip, port)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "PORT command successful.")
}

func handleLIST(conn net.Conn, sess *session.Session, root string, args string) {
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	infos, err := fs.ListDir(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot list directory.")
		return
	}
	listing := fs.FormatList(infos)

	serverSender, err := setupDataSender(sess)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV or PORT first.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK, "Opening data connection for directory listing.")

	serverSender.Send([]byte(listing))
	serverSender.Close()
	cleanupDataConn(sess)

	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Directory listing complete.")
}

func handleRETR(conn net.Conn, sess *session.Session, root string, args string) {
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	file, info, err := fs.OpenForRead(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, fmt.Sprintf("Cannot open %s.", args))
		return
	}
	defer file.Close()

	serverSender, err := setupDataSender(sess)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV or PORT first.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK,
		fmt.Sprintf("Opening data connection for %s (%d bytes).", args, info.Size()))

	buf := make([]byte, 1400)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			if sendErr := serverSender.Send(buf[:n]); sendErr != nil {
				log.Printf("[CTRL] send error: %v", sendErr)
				break
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[CTRL] read error: %v", err)
			serverSender.Close()
			cleanupDataConn(sess)
			tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
			return
		}
	}
	serverSender.Close()
	cleanupDataConn(sess)
	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
}

func handleSTOR(conn net.Conn, sess *session.Session, root string, args string) {
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	file, err := fs.OpenForWrite(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, fmt.Sprintf("Cannot create %s.", args))
		return
	}
	defer file.Close()

	serverReceiver, err := setupDataReceiver(sess)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV or PORT first.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK,
		fmt.Sprintf("Opening data connection for %s.", args))

	for chunk := range serverReceiver.Receive() {
		if _, err := file.Write(chunk); err != nil {
			log.Printf("[CTRL] write error: %v", err)
			serverReceiver.Close()
			cleanupDataConn(sess)
			tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
			return
		}
	}
	serverReceiver.Close()
	cleanupDataConn(sess)

	tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")
}

func handleNOOP(conn net.Conn, sess *session.Session, root string, args string) {
	tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "NOOP ok.")
}

func handleCDUP(conn net.Conn, sess *session.Session, root string, args string) {
	if err := fs.ChangeDir(root, sess, ".."); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot change to parent.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory changed to parent.")
}

func handleMKD(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MKD requires a directory name.")
		return
	}
	if err := fs.MakeDir(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot create directory.")
		return
	}
	absRoot, _ := filepath.Abs(root)
	fullPath := filepath.Join(absRoot, fs.ResolvePath(sess, args))
	tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" created.", fullPath))
}

func handleRMD(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "RMD requires a directory name.")
		return
	}
	if err := fs.RemoveDir(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot remove directory.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory removed.")
}

func handleDELE(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "DELE requires a filename.")
		return
	}
	if err := fs.DeleteFile(root, sess, args); err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot delete file.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "File deleted.")
}

func handleMODE(conn net.Conn, sess *session.Session, root string, args string) {
	if len(args) == 0 {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MODE requires S, B, or C.")
		return
	}
	switch args[0] {
	case 'S', 's':
		tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "Mode set to Stream.")
	case 'B', 'b':
		tcpctrl.WriteReply(conn, tcpctrl.ReplyNotImpl, "Block mode not implemented.")
	case 'C', 'c':
		tcpctrl.WriteReply(conn, tcpctrl.ReplyNotImpl, "Compressed mode not implemented.")
	default:
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MODE must be S, B, or C.")
	}
}

func handleHELP(conn net.Conn, sess *session.Session, root string, args string) {
	commands := "USER PASS QUIT NOOP PWD CWD CDUP MKD RMD LIST NLST STAT SIZE MDTM TYPE MODE PORT PASV RETR STOR STOU APPE DELE RNFR RNTO HASH ABOR HELP"
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyHelp, fmt.Sprintf("Supported commands: %s", commands))
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyHelp, fmt.Sprintf("Help for %s: see RFC 959.", strings.ToUpper(args)))
}

func handleSTAT(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplySystemStatus, "Hybrid FTP server status: running.")
		return
	}
	info, err := fs.StatPath(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot stat path.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, strings.TrimRight(info, "\r\n"))
}

func handleSIZE(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "SIZE requires a filename.")
		return
	}
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	size, err := fs.FileSize(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot get file size.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, fmt.Sprintf("%d", size))
}

func handleMDTM(conn net.Conn, sess *session.Session, root string, args string) {
	if args == "" {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyParamError, "MDTM requires a filename.")
		return
	}
	fullPath, err := fs.ResolveSafe(root, sess, args)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
		return
	}
	mt, err := fs.ModTime(fullPath)
	if err != nil {
		tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot get modification time.")
		return
	}
	tcpctrl.WriteReply(conn, tcpctrl.ReplyFileStatus, mt.Format("20060102150405"))
}
