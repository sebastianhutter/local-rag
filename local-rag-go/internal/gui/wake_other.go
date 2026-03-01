//go:build !darwin

package gui

// RegisterWakeHandler is a no-op on non-macOS platforms.
func RegisterWakeHandler(cb func()) {}
