//go:build darwin

package gui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

extern void RegisterWakeNotification();
*/
import "C"

var wakeCallback func()

// RegisterWakeHandler registers a callback that fires when macOS wakes from sleep.
func RegisterWakeHandler(cb func()) {
	wakeCallback = cb
	C.RegisterWakeNotification()
}

//export goWakeCallback
func goWakeCallback() {
	if wakeCallback != nil {
		wakeCallback()
	}
}
