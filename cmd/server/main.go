package main

import (
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

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
			handleUSER(conn, sess, usersPath, args)
		case tcpctrl.CmdPASS:
			handlePASS(conn, sess, usersPath, args)
		case tcpctrl.CmdQUIT:
			if handleQUIT(conn, sess) {
				return
			}
		case tcpctrl.CmdPWD:
			handlePWD(conn, sess, root)
		case tcpctrl.CmdCWD:
			handleCWD(conn, sess, root, args)
		case tcpctrl.CmdTYPE:
			handleTYPE(conn, sess, args)
		case tcpctrl.CmdPASV:
			handlePASV(conn, sess, args)
		case tcpctrl.CmdPORT:
			handlePORT(conn, sess, root, args)
		case tcpctrl.CmdLIST:
			handleLIST(conn, sess, root, args)
		case tcpctrl.CmdRETR:
			handleRETR(conn, sess, root, args)
		case tcpctrl.CmdSTOR:
			handleSTOR(conn, sess, root, args)
		case tcpctrl.CmdNOOP:
			handleNOOP(conn, sess, root, args)
		case tcpctrl.CmdCDUP:
			handleCDUP(conn, sess, root, args)
		case tcpctrl.CmdMKD:
			handleMKD(conn, sess, root, args)
		case tcpctrl.CmdRMD:
			handleRMD(conn, sess, root, args)
		case tcpctrl.CmdDELE:
			handleDELE(conn, sess, root, args)
		case tcpctrl.CmdMODE:
			handleMODE(conn, sess, root, args)
		case tcpctrl.CmdHELP:
			handleHELP(conn, sess, root, args)
		case tcpctrl.CmdSTAT:
			handleSTAT(conn, sess, root, args)
		case tcpctrl.CmdSIZE:
			handleSIZE(conn, sess, root, args)
		case tcpctrl.CmdMDTM:
			handleMDTM(conn, sess, root, args)
		default:
			tcpctrl.WriteReply(conn, tcpctrl.ReplySyntaxError, "Unknown command.")
		}
	}
}
