//go:build !windows

package state

import "os"

func protectDir(path string) error { return os.Chmod(path, 0700) }
