package main

import (
	"fmt"
	"os"
	"strings"
)

// ANSI styles for console output. Color is switched off when the output isn't
// a console (for example, redirected to a log file), when NO_COLOR is set, or
// with COLOR=off in .env.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiGrey   = "\x1b[90m"
	ansiRed    = "\x1b[91m"
	ansiGreen  = "\x1b[92m"
	ansiYellow = "\x1b[93m"
	ansiBlue   = "\x1b[94m"
	ansiCyan   = "\x1b[96m"
)

var colorEnabled bool

// initColor decides whether to use color. Call it after .env is loaded so
// COLOR=off is seen.
func initColor() {
	if os.Getenv("NO_COLOR") != "" {
		return
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("COLOR")), "off") {
		return
	}
	colorEnabled = enableConsoleColor()
}

func paint(style, s string) string {
	if !colorEnabled || s == "" {
		return s
	}
	return style + s + ansiReset
}

// What each color means in the output.
func errorText(s string) string   { return paint(ansiRed, s) }           // failures
func warnText(s string) string    { return paint(ansiYellow, s) }        // retries and warnings
func okText(s string) string      { return paint(ansiGreen, s) }         // success
func headingText(s string) string { return paint(ansiBold+ansiCyan, s) } // section headings
func itemText(s string) string    { return paint(ansiBold, s) }          // the item being downloaded
func dimText(s string) string     { return paint(ansiGrey, s) }          // skips, folders, details
func indexText(s string) string   { return paint(ansiBlue, s) }          // [03/40] counters

func printHeading(s string) { fmt.Println(headingText(s)) }

func printFinished(model string) { fmt.Println(okText(model + " Finished Downloading...")) }
