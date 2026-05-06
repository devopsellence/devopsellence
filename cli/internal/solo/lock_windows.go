//go:build windows

package solo

import (
	"errors"
	"strings"
)

func (s *StateStore) WithLock(fn func() error) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return errors.New("solo state store path is required")
	}
	return fn()
}
