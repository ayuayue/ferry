package pbproto

import "errors"

// Errors Dial can report, so the UI can tell the user what to do. Both the
// Windows service client and the Linux socket client map their transport
// failures onto this set.
var (
	ErrNoService = errors.New("pbproto: the engine service is not installed or not running")
	ErrBusy      = errors.New("pbproto: another client is already connected to the engine")
	ErrDenied    = errors.New("pbproto: access denied by the engine service")
	ErrBadVer    = errors.New("pbproto: unsupported protocol version")
	ErrClosed    = errors.New("pbproto: connection closed")
	ErrTimeout   = errors.New("pbproto: the engine did not answer")
	ErrRejected  = errors.New("pbproto: the engine rejected the request")
	// ErrDisconnected means the control pipe died underneath us. The service
	// closes it when a rule move points past the end of its rule list.
	ErrDisconnected = errors.New("pbproto: the engine connection was lost")
)
