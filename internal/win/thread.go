package win

import "runtime"

func lockOSThread() { runtime.LockOSThread() }
