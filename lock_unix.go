//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockDirectory(dir string) (*os.File, error) {
	f, e := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("data directory is in use by another server")
	}
	return f, nil
}
