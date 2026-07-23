package tcpctrl

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		line    string
		wantCmd Cmd
		wantArg string
	}{
		{"USER admin", CmdUSER, "admin"},
		{"PASS secret", CmdPASS, "secret"},
		{"QUIT", CmdQUIT, ""},
		{"TYPE A", CmdTYPE, "A"},
		{"PASV", CmdPASV, ""},
		{"RETR file.txt", CmdRETR, "file.txt"},
		{"STOR new.txt", CmdSTOR, "new.txt"},
		{"PWD", CmdPWD, ""},
		{"LIST /tmp", CmdLIST, "/tmp"},
		{"CWD subdir", CmdCWD, "subdir"},
		{"NOOP", CmdNOOP, ""},
		{"CDUP", CmdCDUP, ""},
		{"MKD newdir", CmdMKD, "newdir"},
		{"RMD olddir", CmdRMD, "olddir"},
		{"NLST", CmdNLST, ""},
		{"STAT", CmdSTAT, ""},
		{"SIZE file.txt", CmdSIZE, "file.txt"},
		{"MDTM file.txt", CmdMDTM, "file.txt"},
		{"MODE S", CmdMODE, "S"},
		{"PORT 192,168,1,1,10,20", CmdPORT, "192,168,1,1,10,20"},
		{"STOU", CmdSTOU, ""},
		{"APPE file.txt", CmdAPPE, "file.txt"},
		{"DELE file.txt", CmdDELE, "file.txt"},
		{"RNFR old.txt", CmdRNFR, "old.txt"},
		{"RNTO new.txt", CmdRNTO, "new.txt"},
		{"HASH file.txt", CmdHASH, "file.txt"},
		{"ABOR", CmdABOR, ""},
		{"HELP", CmdHELP, ""},
		{"HELP LIST", CmdHELP, "LIST"},
		{"unknown command", CmdUNKNOWN, "unknown command"},
		{"", CmdUNKNOWN, ""},
		{"user lowercase", CmdUSER, "lowercase"},
		{"paSs mixed", CmdPASS, "mixed"},
	}
	for _, tt := range tests {
		cmd, arg := ParseCommand(tt.line)
		if cmd != tt.wantCmd {
			t.Errorf("ParseCommand(%q) cmd = %v, want %v", tt.line, cmd, tt.wantCmd)
		}
		if arg != tt.wantArg {
			t.Errorf("ParseCommand(%q) arg = %q, want %q", tt.line, arg, tt.wantArg)
		}
	}
}
