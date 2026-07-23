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
	CmdNOOP
	CmdCDUP
	CmdMKD
	CmdRMD
	CmdNLST
	CmdSTAT
	CmdSIZE
	CmdMDTM
	CmdMODE
	CmdPORT
	CmdSTOU
	CmdAPPE
	CmdDELE
	CmdRNFR
	CmdRNTO
	CmdHASH
	CmdABOR
	CmdHELP
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
	"NOOP": CmdNOOP,
	"CDUP": CmdCDUP,
	"MKD":  CmdMKD,
	"RMD":  CmdRMD,
	"NLST": CmdNLST,
	"STAT": CmdSTAT,
	"SIZE": CmdSIZE,
	"MDTM": CmdMDTM,
	"MODE": CmdMODE,
	"PORT": CmdPORT,
	"STOU": CmdSTOU,
	"APPE": CmdAPPE,
	"DELE": CmdDELE,
	"RNFR": CmdRNFR,
	"RNTO": CmdRNTO,
	"HASH": CmdHASH,
	"ABOR": CmdABOR,
	"HELP": CmdHELP,
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
