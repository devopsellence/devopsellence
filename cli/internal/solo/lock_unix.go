//go:build !windows

package solo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func (s *StateStore) WithLock(fn func() error) error {
	return s.WithLockNotify(fn, nil)
}

func (s *StateStore) WithLockNotify(fn func() error, waiting func() error) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return errors.New("solo state store path is required")
	}
	lockPath := s.Path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open solo state lock: %w", err)
	}
	defer lockFile.Close()
	fd := int(lockFile.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return fmt.Errorf("lock solo state: %w", err)
		}
		if waiting != nil {
			if waitErr := waiting(); waitErr != nil {
				return waitErr
			}
		}
		if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
			return fmt.Errorf("lock solo state: %w", err)
		}
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	return fn()
}
