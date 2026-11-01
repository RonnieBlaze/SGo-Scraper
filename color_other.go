//go:build !windows

package main

import "os"

// enableConsoleColor reports whether stdout is a terminal that can show color.
func enableConsoleColor() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0 && os.Getenv("TERM") != "dumb"
}
