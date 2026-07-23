package tcpctrl

import "testing"

func TestFormatReply(t *testing.T) {
	tests := []struct {
		code int
		msg  string
		want string
	}{
		{220, "Hybrid FTP Ready.", "220 Hybrid FTP Ready.\r\n"},
		{331, "Password required.", "331 Password required.\r\n"},
		{230, "Login successful.", "230 Login successful.\r\n"},
		{150, "Opening data connection.", "150 Opening data connection.\r\n"},
		{226, "Transfer complete.", "226 Transfer complete.\r\n"},
		{500, "Syntax error.", "500 Syntax error.\r\n"},
	}
	for _, tt := range tests {
		got := FormatReply(tt.code, tt.msg)
		if got != tt.want {
			t.Errorf("FormatReply(%d, %q) = %q, want %q", tt.code, tt.msg, got, tt.want)
		}
	}
}

func TestReplyConstants_Unique(t *testing.T) {
	codes := map[int]bool{
		ReplyReady:           true,
		ReplyLoggedIn:        true,
		ReplyNeedPassword:    true,
		ReplyNotLoggedIn:     true,
		ReplyOK:              true,
		ReplyFileOK:          true,
		ReplyClosingData:     true,
		ReplyPASV:            true,
		ReplyQuit:            true,
		ReplyFileActionOK:    true,
		ReplyPathCreated:     true,
		ReplySyntaxError:     true,
		ReplyBadSequence:     true,
		ReplyNotImpl:         true,
		ReplyFileUnavail:     true,
		ReplyCantOpenData:    true,
		ReplyTransferAborted: true,
	}
	for c := range codes {
		if codes[c] != true {
			t.Errorf("duplicate reply code: %d", c)
		}
	}
}
