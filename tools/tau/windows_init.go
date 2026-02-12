//go:build windows

package main

import "os"

func init() {
	// Prevent MSYS2/Git Bash from converting Unix-style paths when spawning
	// subprocesses (e.g. /c/Users -> C:\Users). Required for correct path handling.
	os.Setenv("MSYS_NO_PATHCONV", "1")
}
