// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"log"
	"os"
	"syscall"
)

// attachConsole: the release exe is built -H windowsgui (no console window
// behind the bookcase), so a command run from cmd or PowerShell borrows the console
// it was started from; otherwise its output would go nowhere. The prompt comes
// back straight away, so output lands after it: a GUI-exe quirk, harmless.
//
// Output that's already going somewhere (redirected to a file, piped, or
// run over SSH with a redirect) is left alone: borrowing the console would
// send it to the wrong place.
func attachConsole() {
	if hasOutput() {
		return
	}
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole").Call(attachParentProcess); r == 0 {
		return // no parent console (double-clicked) or already has one (go run)
	}
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = f, f
		log.SetOutput(f)
	}
}

// hasOutput reports whether stdout is already a real file, pipe or console.
func hasOutput() bool {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || h == syscall.InvalidHandle || h == 0 {
		return false
	}
	t, err := syscall.GetFileType(h)
	return err == nil && t != syscall.FILE_TYPE_UNKNOWN
}
