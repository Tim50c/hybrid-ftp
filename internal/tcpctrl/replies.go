package tcpctrl

import "fmt"

const (
	ReplyReady           = 220
	ReplyLoggedIn        = 230
	ReplyNeedPassword    = 331
	ReplyNotLoggedIn     = 530
	ReplyOK              = 200
	ReplyFileOK          = 150
	ReplyClosingData     = 226
	ReplyPASV            = 227
	ReplyQuit            = 221
	ReplyFileActionOK    = 250
	ReplyPathCreated     = 257
	ReplySyntaxError     = 500
	ReplyBadSequence     = 503
	ReplyNotImpl         = 504
	ReplyFileUnavail     = 550
	ReplyCantOpenData    = 425
	ReplyTransferAborted = 426
)

func FormatReply(code int, msg string) string {
	return fmt.Sprintf("%d %s\r\n", code, msg)
}
