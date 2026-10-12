package main

import "testing"

// The D6 death signature the failure matrix asserts (issue #217) requires a
// fresh daemon boot to report pi_running:false; the smoke image disables pi
// autostart via the environment.
func TestPiAutostartEnv(t *testing.T) {
	t.Setenv("KAPE_PI_AUTOSTART", "0")
	if piAutostart() {
		t.Error("KAPE_PI_AUTOSTART=0 must disable pi autostart")
	}
	t.Setenv("KAPE_PI_AUTOSTART", "")
	if !piAutostart() {
		t.Error("unset KAPE_PI_AUTOSTART must default to pi autostart")
	}
}
