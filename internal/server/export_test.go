package server

import "time"

// SetHookTimeout shortens the notify hooks' timeout for a test; it returns the way back.
func SetHookTimeout(d time.Duration) (restore func()) {
	old := hookTimeout
	hookTimeout = d
	return func() { hookTimeout = old }
}
