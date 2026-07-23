package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"hybrid-ftp/internal/auth"
	"hybrid-ftp/internal/fs"
	"hybrid-ftp/internal/rdt"
	"hybrid-ftp/internal/session"
	"hybrid-ftp/internal/tcpctrl"
)

var (
	ctrlPort  = flag.String("ctrl", ":2121", "TCP control port")
	rootDir   = flag.String("root", "/tmp/hybrid-ftp", "Virtual filesystem root")
	usersFile = flag.String("users", "users.conf", "Path to users.conf")
)

func main() {
	flag.Parse()

	if err := os.MkdirAll(*rootDir, 0755); err != nil {
		log.Fatalf("cannot create root directory: %v", err)
	}

	mgr := session.NewManager()

	listener, err := net.Listen("tcp", *ctrlPort)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *ctrlPort, err)
	}
	log.Printf("[CTRL] server listening on %s, root=%s", *ctrlPort, *rootDir)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("[CTRL] shutting down...")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[CTRL] accept error: %v", err)
			break
		}
		go handleSession(conn, mgr, *rootDir, *usersFile)
	}
}

func handleSession(conn net.Conn, mgr *session.Manager, root, usersPath string) {
	sess := mgr.Register(conn)
	defer mgr.Unregister(sess.ID)
	defer conn.Close()

	log.Printf("[CTRL] session %d connected from %s", sess.ID, conn.RemoteAddr())

	if err := tcpctrl.WriteReply(conn, tcpctrl.ReplyReady, "Hybrid FTP Ready."); err != nil {
		return
	}

	for {
		line, err := tcpctrl.ReadLine(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("[CTRL] session %d read error: %v", sess.ID, err)
			}
			return
		}
		if line == "" {
			continue
		}

		log.Printf("[CTRL] session %d cmd: %s", sess.ID, line)

		cmd, args := tcpctrl.ParseCommand(line)

		if !sess.Authenticated && cmd != tcpctrl.CmdUSER && cmd != tcpctrl.CmdPASS && cmd != tcpctrl.CmdQUIT {
			tcpctrl.WriteReply(conn, tcpctrl.ReplyNotLoggedIn, "Login first.")
			continue
		}

		switch cmd {
		case tcpctrl.CmdUSER:
			sess.Username = args
			tcpctrl.WriteReply(conn, tcpctrl.ReplyNeedPassword, "Password required.")

		case tcpctrl.CmdPASS:
			if sess.Authenticated {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Already logged in.")
				continue
			}
			if auth.CheckPassword(usersPath, sess.Username, args) {
				sess.Authenticated = true
				tcpctrl.WriteReply(conn, tcpctrl.ReplyLoggedIn, "Login successful.")
			} else {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyNotLoggedIn, "Login failed.")
			}

		case tcpctrl.CmdQUIT:
			tcpctrl.WriteReply(conn, tcpctrl.ReplyQuit, "Goodbye.")
			return

		case tcpctrl.CmdPWD:
			absRoot, _ := filepath.Abs(root)
			cwd := filepath.Join(absRoot, sess.CWD)
			tcpctrl.WriteReply(conn, tcpctrl.ReplyPathCreated, fmt.Sprintf("\"%s\" is current directory.", cwd))

		case tcpctrl.CmdCWD:
			fullPath, err := fs.ResolveSafe(root, sess, args)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
				continue
			}
			info, err := os.Stat(fullPath)
			if err != nil || !info.IsDir() {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Directory not found.")
				continue
			}
			sess.CWD = fs.ResolvePath(sess, args)
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileActionOK, "Directory changed.")

		case tcpctrl.CmdTYPE:
			if len(args) > 0 && (args[0] == 'A' || args[0] == 'a') {
				sess.TransferType = 'A'
			} else if len(args) > 0 && (args[0] == 'I' || args[0] == 'i') {
				sess.TransferType = 'I'
			} else {
				tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "TYPE must be A or I.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyOK, "Type set.")

		case tcpctrl.CmdPASV:
			udpAddr, err := net.ResolveUDPAddr("udp", ":0")
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "Cannot bind UDP.")
				continue
			}
			udpConn, err := net.ListenUDP("udp", udpAddr)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "Cannot bind UDP.")
				continue
			}
			if sess.DataConn != nil {
				sess.DataConn.Close()
			}
			sess.DataConn = udpConn

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

		case tcpctrl.CmdLIST:
			fullPath, err := fs.ResolveSafe(root, sess, args)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
				continue
			}
			infos, err := fs.ListDir(fullPath)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Cannot list directory.")
				continue
			}
			listing := fs.FormatList(infos)

			if sess.DataConn == nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV first.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK, "Opening data connection for directory listing.")

			serverSender := rdt.NewSender(sess.DataConn, sess.ID)
			if err := serverSender.Accept(); err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "RDT handshake failed.")
				continue
			}
			serverSender.Send([]byte(listing))
			serverSender.Close()

			tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Directory listing complete.")

		case tcpctrl.CmdRETR:
			fullPath, err := fs.ResolveSafe(root, sess, args)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
				continue
			}
			file, info, err := fs.OpenForRead(fullPath)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, fmt.Sprintf("Cannot open %s.", args))
				continue
			}
			defer file.Close()

			if sess.DataConn == nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV first.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK,
				fmt.Sprintf("Opening data connection for %s (%d bytes).", args, info.Size()))

			serverSender := rdt.NewSender(sess.DataConn, sess.ID)
			if err := serverSender.Accept(); err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "RDT handshake failed.")
				continue
			}

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
					tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
					return
				}
			}
			serverSender.Close()
			tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")

		case tcpctrl.CmdSTOR:
			fullPath, err := fs.ResolveSafe(root, sess, args)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, "Access denied.")
				continue
			}
			file, err := fs.OpenForWrite(fullPath)
			if err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyFileUnavail, fmt.Sprintf("Cannot create %s.", args))
				continue
			}
			defer file.Close()

			if sess.DataConn == nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyBadSequence, "Use PASV first.")
				continue
			}
			tcpctrl.WriteReply(conn, tcpctrl.ReplyFileOK,
				fmt.Sprintf("Opening data connection for %s.", args))

			serverReceiver := rdt.NewReceiver(sess.DataConn, sess.ID)
			if err := serverReceiver.Accept(); err != nil {
				tcpctrl.WriteReply(conn, tcpctrl.ReplyCantOpenData, "RDT handshake failed.")
				continue
			}

			for chunk := range serverReceiver.Receive() {
				if _, err := file.Write(chunk); err != nil {
					log.Printf("[CTRL] write error: %v", err)
					serverReceiver.Close()
					tcpctrl.WriteReply(conn, tcpctrl.ReplyTransferAborted, "Transfer aborted.")
					return
				}
			}
			serverReceiver.Close()

			tcpctrl.WriteReply(conn, tcpctrl.ReplyClosingData, "Transfer complete.")

		default:
			tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "Unknown command.")
		}
	}
}
