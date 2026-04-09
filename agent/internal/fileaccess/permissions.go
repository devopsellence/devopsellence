package fileaccess

import (
	"os"
	"syscall"
)

func EnsureDirMode(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return ensureMode(path, mode)
}

func EnsureDirOwnershipAndMode(path string, mode os.FileMode, uid int, gid int) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return EnsureOwnershipAndMode(path, mode, uid, gid)
}

func EnsureOwnershipAndMode(path string, mode os.FileMode, uid int, gid int) error {
	if err := ensureMode(path, mode); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && int(stat.Uid) == uid && int(stat.Gid) == gid {
		return nil
	}
	return os.Chown(path, uid, gid)
}

func ensureMode(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() == mode {
		return nil
	}
	return os.Chmod(path, mode)
}
