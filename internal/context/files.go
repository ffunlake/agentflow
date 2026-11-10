package context

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// openAbsolute walks from / using directory handles, rejecting symlinks even
// when they point inside the tree. Public ancestors are allowed; the context
// base and every directory beneath it must be exactly 0700.
func openAbsolute(path string) (*os.Root, error) {
	if !absolute(path) || path == "/" {
		return nil, fmt.Errorf("%w: expected a canonical absolute directory", ErrInvalid)
	}
	r, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := child(r, part, false)
		r.Close()
		if err != nil {
			return nil, err
		}
		r = next
	}
	if err := private(r); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func child(r *os.Root, name string, requirePrivate bool) (*os.Root, error) {
	before, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: directory must not be a symlink", ErrUnsafe)
	}
	next, err := r.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := next.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		next.Close()
		return nil, fmt.Errorf("%w: directory changed during open", ErrUnsafe)
	}
	if requirePrivate {
		if err := private(next); err != nil {
			next.Close()
			return nil, err
		}
	}
	return next, nil
}

func private(r *os.Root) error {
	info, err := r.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode() != os.ModeDir|0700 {
		return fmt.Errorf("%w: directory requires mode 0700", ErrUnsafe)
	}
	return nil
}

func readFile(r *os.Root, name string) ([]byte, error) {
	info, err := r.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("%w: missing context file: %w", ErrCorrupt, err)
	}
	if !info.Mode().IsRegular() || info.Mode() != 0600 {
		return nil, fmt.Errorf("%w: file requires regular mode 0600", ErrUnsafe)
	}
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode() != 0600 {
		return nil, fmt.Errorf("%w: context file changed during open", ErrUnsafe)
	}
	body, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFileSize {
		return nil, fmt.Errorf("%w: oversized context file", ErrCorrupt)
	}
	return body, nil
}

func decode(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("%w: JSON schema: %v", ErrCorrupt, err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON data", ErrCorrupt)
	}
	return nil
}

// publish writes and syncs a temporary file, then uses link for atomic,
// no-replacement publication. An existing target, including a symlink, fails.
func publish(r *os.Root, name string, body []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := ".tmp-" + hex.EncodeToString(nonce[:])
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := r.Link(tmp, name); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrExists, name)
		}
		return err
	}
	return syncDir(r)
}

func syncDir(r *os.Root) error {
	f, err := r.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func encode(v any) ([]byte, error) {
	body, err := json.Marshal(v)
	return append(body, '\n'), err
}

// createBase creates only the configured leaf. Missing parents are errors;
// existing parents are never chmodded or recursively created.
func createBase(path string) (*os.Root, error) {
	if !absolute(path) || path == "/" {
		return nil, fmt.Errorf("%w: context_dir must be canonical and absolute", ErrInvalid)
	}
	parent := filepath.Dir(path)
	// The parent may be public. Walk it without imposing private mode on it.
	r, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	if parent != "/" {
		for _, part := range strings.Split(strings.TrimPrefix(parent, "/"), "/") {
			next, err := child(r, part, false)
			r.Close()
			if err != nil {
				return nil, err
			}
			r = next
		}
	}
	defer r.Close()
	if err := r.Mkdir(filepath.Base(path), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return child(r, filepath.Base(path), true)
}
