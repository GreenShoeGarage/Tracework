//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-only
package main

import "syscall"

func diskFree(path string) uint64 {
	var s syscall.Statfs_t
	if syscall.Statfs(path, &s) != nil {
		return 0
	}
	return s.Bavail * uint64(s.Bsize)
}
