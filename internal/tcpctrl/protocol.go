package tcpctrl

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
)

func ReadLine(conn net.Conn) (string, error) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read line: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	return line, nil
}

func WriteReply(conn net.Conn, code int, msg string) error {
	reply := FormatReply(code, msg)
	_, err := io.WriteString(conn, reply)
	if err != nil {
		return fmt.Errorf("write reply: %w", err)
	}
	return nil
}
