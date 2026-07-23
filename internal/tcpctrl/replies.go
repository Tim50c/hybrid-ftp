package tcpctrl

import "fmt"

const (
	ReplyReady              = 220
	ReplyLoggedIn           = 230
	ReplyNeedPassword       = 331
	ReplyNotLoggedIn        = 530
	ReplyOK                 = 200
	ReplyFileOK             = 150
	ReplyClosingData        = 226
	ReplyPASV               = 227
	ReplyQuit               = 221
	ReplyFileActionOK       = 250
	ReplyPathCreated        = 257
	ReplySyntaxError        = 500
	ReplyBadSequence        = 503
	ReplyNotImpl            = 504
	ReplyFileUnavail        = 550
	ReplyCantOpenData       = 425
	ReplyTransferAborted    = 426
	ReplySystemStatus       = 211
	ReplyDirStatus          = 212
	ReplyFileStatus         = 213
	ReplyHelp               = 214
	ReplyPendingRNTO        = 350
	ReplyParamError         = 501
	ReplyNotImplCmd         = 502
	ReplyFilenameNotAllowed = 553
)

func FormatReply(code int, msg string) string {
	return fmt.Sprintf("%d %s\r\n", code, msg)
}
