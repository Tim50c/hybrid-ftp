package tcpctrl

import "strings"

type Cmd int

const (
	CmdUSER Cmd = iota
	CmdPASS
	CmdQUIT
	CmdTYPE
	CmdPASV
	CmdRETR
	CmdSTOR
	CmdPWD
	CmdLIST
	CmdCWD
	CmdUNKNOWN
)

var commandNames = map[string]Cmd{
	"USER": CmdUSER,
	"PASS": CmdPASS,
	"QUIT": CmdQUIT,
	"TYPE": CmdTYPE,
	"PASV": CmdPASV,
	"RETR": CmdRETR,
	"STOR": CmdSTOR,
	"PWD":  CmdPWD,
	"LIST": CmdLIST,
	"CWD":  CmdCWD,
}

func ParseCommand(line string) (Cmd, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return CmdUNKNOWN, ""
	}
	parts := strings.SplitN(line, " ", 2)
	name := strings.ToUpper(parts[0])
	cmd, ok := commandNames[name]
	if !ok {
		if len(parts) > 1 {
			return CmdUNKNOWN, line
		}
		return CmdUNKNOWN, parts[0]
	}
	if len(parts) > 1 {
		return cmd, parts[1]
	}
	return cmd, ""
}
