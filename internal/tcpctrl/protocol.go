package tcpctrl

import (
	"fmt"
	"io"
	"net"
	"strings"
)

func ReadLine(conn net.Conn) (string, error) {
	var buf [1]byte
	var line []byte
	for {
		_, err := conn.Read(buf[:])
		if err != nil {
			return "", fmt.Errorf("read line: %w", err)
		}
		line = append(line, buf[0])
		if buf[0] == '\n' {
			break
		}
	}
	return strings.TrimRight(string(line), "\r\n"), nil
}

func WriteReply(conn net.Conn, code int, msg string) error {
	reply := FormatReply(code, msg)
	_, err := io.WriteString(conn, reply)
	if err != nil {
		return fmt.Errorf("write reply: %w", err)
	}
	return nil
}
