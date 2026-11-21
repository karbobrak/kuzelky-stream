//go:build windows

// Package win wraps the few Windows calls the app needs. None of them requires admin rights.
package win

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
	procCreateMutexW            = kernel32.NewProc("CreateMutexW")
)

const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esDisplayRequired  = 0x00000002
	errorAlreadyExists = 183
)

// KeepAwake stops Windows from sleeping and turning off the display (on=true) or allows it again.
// The request belongs to the calling OS thread, so it is made from a locked goroutine.
func KeepAwake(on bool) {
	flags := uintptr(esContinuous)
	if on {
		flags |= esSystemRequired | esDisplayRequired
	}
	keepAwakeThread <- flags
}

// ES_CONTINUOUS applies to the calling thread, so all calls go through one locked thread.
var keepAwakeThread = func() chan uintptr {
	ch := make(chan uintptr)
	go func() {
		lockOSThread()
		for f := range ch {
			procSetThreadExecutionState.Call(f)
		}
	}()
	return ch
}()

// SingleInstance returns an error if another copy of the app is already running.
// The returned handle must stay alive for the lifetime of the process (it is released on exit).
func SingleInstance(name string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return 0, callErr
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		return h, errors.New("aplikace už běží")
	}
	return h, nil
}

var procMessageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

const (
	mbOK         = 0x0
	mbYesNo      = 0x4
	mbIconError  = 0x10
	mbIconQuest  = 0x20
	mbIconInfo   = 0x40
	mbTopmost    = 0x40000
	mbSetForegnd = 0x10000
	idYes        = 6
	messageTitle = "Kuželky Stream"
)

func messageBox(text string, flags uintptr) uintptr {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(messageTitle)
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)),
		flags|mbTopmost|mbSetForegnd)
	return r
}

// ShowError shows a blocking error dialog (the app has no console window).
func ShowError(text string) { messageBox(text, mbOK|mbIconError) }

// ShowInfo shows a blocking information dialog.
func ShowInfo(text string) { messageBox(text, mbOK|mbIconInfo) }

// Confirm asks a yes/no question.
func Confirm(text string) bool { return messageBox(text, mbYesNo|mbIconQuest) == idYes }

// OpenBrowser opens url in the default browser.
func OpenBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
