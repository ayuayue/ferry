package svc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var messageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// ShowStartupError reports a pre-window fatal error without starting WebView2.
func ShowStartupError(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("Ferry 无法启动")
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10) // MB_ICONERROR
}
