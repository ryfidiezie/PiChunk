package main

import (
	"syscall"
)

func init() {
	var mode uint32
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err == nil {
		syscall.GetConsoleMode(h, &mode)
		mode |= 4 // ENABLE_VIRTUAL_TERMINAL_PROCESSING
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		setConsoleMode := kernel32.NewProc("SetConsoleMode")
		setConsoleMode.Call(uintptr(h), uintptr(mode))
	}
}
