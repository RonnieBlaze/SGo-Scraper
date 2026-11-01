//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableConsoleColor turns on ANSI escape handling in the Windows console
// (needed for the classic console the watcher runs in). It returns false when
// stdout isn't a console or the console doesn't support it.
func enableConsoleColor() bool {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
