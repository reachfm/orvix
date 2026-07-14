package smtp

import (
	"fmt"
)

// SMTP response codes.
const (
	StatusReady               = 220
	StatusClosing             = 221
	StatusAuthSuccess         = 235
	StatusOK                  = 250
	StatusNotLocal            = 251
	StatusConfirm             = 252
	StatusAuthChallenge       = 334
	StatusStartData           = 354
	StatusServiceNotAvailable = 421
	StatusAuthRequired        = 530
	StatusAuthFailed          = 535
	StatusMailboxUnavailable  = 450
	StatusMailboxFull         = 452
	StatusInvalidCmd          = 500
	StatusBadArgs             = 501
	StatusNotImplemented      = 502
	StatusBadSequence         = 503
	StatusParamNotImplemented = 504
	StatusMailboxNotFound     = 550
	StatusMailboxNotLocal     = 551
	StatusExceededStorage     = 552
	StatusMessageTooLarge     = 552
	StatusMailboxNameInvalid  = 553
	StatusTransactionFailed   = 554
	StatusTLSNotAvailable     = 454
	StatusTLSFailed           = 451
)

// Response represents an SMTP response line.
type Response struct {
	Code    int
	Message string
}

func (r Response) String() string {
	if r.Message == "" {
		return fmt.Sprintf("%d\r\n", r.Code)
	}
	return fmt.Sprintf("%d %s\r\n", r.Code, r.Message)
}

// MultiLine creates a multi-line SMTP response.
func MultiLine(code int, lines []string) string {
	if len(lines) == 0 {
		return fmt.Sprintf("%d \r\n", code)
	}
	result := ""
	for i, line := range lines {
		sep := "-"
		if i == len(lines)-1 {
			sep = " "
		}
		result += fmt.Sprintf("%d%s%s\r\n", code, sep, line)
	}
	return result
}

// Standard responses.
var (
	ResponseReady           = Response{StatusReady, "Orvix Mail Server ESMTP"}
	ResponseBye             = Response{StatusClosing, "2.0.0 Bye"}
	ResponseOK              = Response{StatusOK, "2.0.0 OK"}
	ResponseAuthSuccess     = Response{StatusAuthSuccess, "2.7.0 Authentication successful"}
	ResponseStartData       = Response{StatusStartData, "Start mail input; end with <CRLF>.<CRLF>"}
	ResponseMessageOK       = MessageAccepted
	ResponseBadSequence     = Response{StatusBadSequence, "5.5.0 Bad sequence of commands"}
	ResponseCmdUnknown      = Response{StatusInvalidCmd, "5.5.1 Command unrecognized"}
	ResponseSyntaxErr       = Response{StatusBadArgs, "5.5.2 Syntax error"}
	ResponseNoFrom          = Response{StatusBadSequence, "5.5.1 MAIL FROM required first"}
	ResponseNoRecipient     = Response{StatusBadSequence, "5.5.1 RCPT TO required first"}
	ResponseNoRelay         = Response{StatusMailboxNotFound, "5.7.1 Relay not permitted"}
	ResponseAuthFail        = Response{StatusAuthFailed, "5.7.8 Authentication failed"}
	ResponseAuthReq         = Response{StatusAuthRequired, "5.7.1 Authentication required"}
	ResponseNotLocal        = Response{StatusMailboxNotLocal, "5.1.2 Domain not hosted here"}
	ResponseTempFail        = Response{StatusTransactionFailed, "5.0.0 Temporary server error"}
	ResponseSizeExceeded    = Response{StatusMessageTooLarge, "5.3.4 Message too large"}
	ResponseMailboxNotFound = Response{StatusMailboxNotFound, "5.1.1 User unknown"}
)

var MessageAccepted = Response{StatusOK, "2.0.0 Message accepted for delivery"}

func responsef(code int, format string, args ...interface{}) Response {
	return Response{code, fmt.Sprintf(format, args...)}
}
