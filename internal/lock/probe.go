package lock

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

type SocketStatus string

const (
	SocketMissing     SocketStatus = "MISSING"
	SocketConnectable SocketStatus = "CONNECTABLE"
	SocketRefused     SocketStatus = "REFUSED"
	SocketTimeout     SocketStatus = "TIMEOUT"
	SocketCanceled    SocketStatus = "CANCELED"
	SocketError       SocketStatus = "ERROR"
)

type SocketProbe struct {
	Path   string       `json:"path"`
	Status SocketStatus `json:"status"`
	Error  string       `json:"error,omitempty"`
}

// ProbeSocket checks a Unix socket with a bounded connect, then closes without
// sending any bytes. CONNECTABLE proves only transport reachability, not owner
// identity, protocol health, business execution or coordinator readiness. It
// never unlinks a socket/lock or attempts recovery. Nonpositive timeout uses 1s;
// an earlier context deadline still wins.
func ProbeSocket(ctx context.Context, path string, timeout time.Duration) SocketProbe {
	result := SocketProbe{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		result.Status = socketErrorStatus(err)
		result.Error = err.Error()
		return result
	}
	if info.Mode()&os.ModeSocket == 0 {
		result.Status = SocketError
		result.Error = "path is not a Unix socket"
		return result
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		result.Status = socketErrorStatus(err)
		result.Error = err.Error()
		return result
	}
	_ = conn.Close()
	result.Status = SocketConnectable
	return result
}

func socketErrorStatus(err error) SocketStatus {
	var timeout net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return SocketCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return SocketTimeout
	case errors.Is(err, os.ErrNotExist):
		return SocketMissing
	case errors.Is(err, syscall.ECONNREFUSED):
		return SocketRefused
	default:
		return SocketError
	}
}

type PIDStatus string

const (
	PIDAlive   PIDStatus = "ALIVE"
	PIDDead    PIDStatus = "DEAD"
	PIDUnknown PIDStatus = "UNKNOWN"
)

type PIDProbe struct {
	PID    int       `json:"pid"`
	Status PIDStatus `json:"status"`
	Error  string    `json:"error,omitempty"`
}

// ProbePID is diagnostic only. Signal 0 sends no signal. ALIVE cannot verify an
// owner's identity (PIDs can be reused); DEAD never authorizes lock takeover.
func ProbePID(pid int) PIDProbe {
	result := PIDProbe{PID: pid, Status: PIDUnknown}
	if pid <= 0 {
		result.Error = "PID must be positive"
		return result
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		result.Status = PIDAlive
	case errors.Is(err, syscall.ESRCH):
		result.Status = PIDDead
	default:
		result.Error = err.Error()
	}
	return result
}
