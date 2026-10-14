package collector

import (
	"crypto/rand"
	"cyd-token-usage-meter/internal/usage"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var errStorage = errors.New("storage_unavailable")
var errLocked = errors.New("collector_already_running")

type Store struct {
	config Config
	key    []byte
	locks  []*os.File
	// Injection seam for interrupted-write tests; production uses os.Rename.
	rename func(string, string) error
}

func OpenStore(c Config) (*Store, error) {
	s := &Store{config: c, rename: os.Rename}
	for _, dir := range []struct {
		path string
		mode os.FileMode
	}{{c.StateDir, 0700}, {c.DataDir, 0750}} {
		if err := os.MkdirAll(dir.path, dir.mode); err != nil {
			s.Close()
			return nil, errStorage
		}
		info, err := os.Stat(dir.path)
		if err != nil || !info.IsDir() || info.Mode().Perm() & ^dir.mode != 0 {
			s.Close()
			return nil, errStorage
		}
		f, err := openNoFollow(filepath.Join(dir.path, ".collector.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			s.Close()
			return nil, errStorage
		}
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			s.Close()
			return nil, errLocked
		}
		s.locks = append(s.locks, f)
	}
	keyPath := filepath.Join(c.StateDir, "installation.key")
	key, err := usage.ReadFile(keyPath, 32)
	if err != nil && !os.IsNotExist(err) {
		s.Close()
		return nil, errStorage
	}
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			s.Close()
			return nil, errStorage
		}
		if err = s.atomicWrite(keyPath, key, 0600); err != nil {
			s.Close()
			return nil, err
		}
	} else {
		info, statErr := os.Lstat(keyPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || len(key) != 32 {
			s.Close()
			return nil, errStorage
		}
	}
	s.key = key
	return s, nil
}

func (s *Store) Close() {
	for _, f := range s.locks {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	s.locks = nil
}

func openNoFollow(path string, flags int, mode os.FileMode) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, uint32(mode))
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errStorage
	}
	return f, nil
}

// Fixed, per-target temporary names plus exclusive directory locks bound debris
// even across SIGKILL. The destination is untouched until a complete fsynced file
// is renamed on the same filesystem. Never follow a leftover symlink.
func (s *Store) atomicWrite(path string, b []byte, mode os.FileMode) error {
	temp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	f, err := openNoFollow(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return errStorage
	}
	defer os.Remove(temp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errStorage
	}
	if err = s.rename(temp, path); err != nil {
		return errStorage
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errStorage
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return errStorage
	}
	return nil
}
