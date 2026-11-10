package lock

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func socketPath(t *testing.T) string {
	t.Helper()
	// Keep sockaddr_un within macOS's limit even under the project TMPDIR.
	dir, err := os.MkdirTemp("", "lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func requireSocketStatus(t *testing.T, probe SocketProbe, path string, status SocketStatus) {
	t.Helper()
	if probe.Path != path || probe.Status != status || (status != SocketConnectable && probe.Error == "") {
		t.Fatalf("socket probe = %+v; want %s", probe, status)
	}
}

func TestProbeSocketLifecycle(t *testing.T) {
	path := socketPath(t)
	requireSocketStatus(t, ProbeSocket(context.Background(), path, time.Second), path, SocketMissing)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	requireSocketStatus(t, ProbeSocket(context.Background(), path, 0), path, SocketConnectable)
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(conn)
	if err != nil || len(data) != 0 {
		t.Fatalf("probe sent application bytes: %q, %v", data, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	requireSocketStatus(t, ProbeSocket(context.Background(), path, time.Second), path, SocketMissing)
}

func TestProbeRefusedSocketPreservesLockAndSocket(t *testing.T) {
	path := socketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(filepath.Dir(path), "run.lock")
	acquire(t, lockPath)
	before := readFile(t, lockPath)
	requireSocketStatus(t, ProbeSocket(context.Background(), path, time.Second), path, SocketRefused)
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("probe removed closed socket: %v", err)
	}
	if !bytes.Equal(before, readFile(t, lockPath)) {
		t.Fatal("probe changed lock")
	}
	requireConflict(t, lockPath)
}

func TestProbeDeadlineCancellationAndNonSocket(t *testing.T) {
	path := socketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	lockPath := filepath.Join(filepath.Dir(path), "run.lock")
	acquire(t, lockPath)
	before := readFile(t, lockPath)
	// An expired caller deadline deterministically exercises real DialContext's
	// timeout path without relying on OS-specific Unix socket backlog limits.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	requireSocketStatus(t, ProbeSocket(ctx, path, time.Second), path, SocketTimeout)
	canceled, stop := context.WithCancel(context.Background())
	stop()
	requireSocketStatus(t, ProbeSocket(canceled, path, time.Second), path, SocketCanceled)
	if !bytes.Equal(before, readFile(t, lockPath)) {
		t.Fatal("timeout/cancellation changed lock")
	}
	requireConflict(t, lockPath)
	requireSocketStatus(t, ProbeSocket(context.Background(), lockPath, time.Second), lockPath, SocketError)
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	requireSocketStatus(t, ProbeSocket(context.Background(), link, time.Second), link, SocketError)
}

func TestProbePIDDiagnostic(t *testing.T) {
	if got := ProbePID(os.Getpid()); got.PID != os.Getpid() || got.Status != PIDAlive || got.Error != "" {
		t.Fatalf("current PID = %+v", got)
	}
	for _, pid := range []int{0, -1} {
		if got := ProbePID(pid); got.Status != PIDUnknown || got.Error == "" {
			t.Fatalf("invalid PID = %+v", got)
		}
	}
}
