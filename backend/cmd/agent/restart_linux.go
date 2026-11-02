//go:build linux

package main

import (
	"os"
	"syscall"
)

func restartSelf(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
