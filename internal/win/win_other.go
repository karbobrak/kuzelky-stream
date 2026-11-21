//go:build !windows

package win

import "os/exec"

func KeepAwake(bool) {}

func SingleInstance(string) (uintptr, error) { return 0, nil }

func OpenBrowser(url string) error { return exec.Command("xdg-open", url).Start() }

func ShowError(text string)    { println(text) }
func ShowInfo(text string)     { println(text) }
func Confirm(text string) bool { return true }
func Version() string          { return "?" }
