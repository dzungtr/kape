package main

import (
	"log"
	"os"
	"testing"
)

// TestMain mirrors main()'s fakepi dispatch: os.Executable() inside a test is
// the test binary, so NewPi(nil, …) with a pinned session id (spec #245 E3)
// resolves the built-in fake pi to this test binary — serve the fakepi
// subcommand instead of re-running the suite (TestFakePiSessionFlagsStartsSelf).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "fakepi" {
		if err := runFakePi(os.Stdin, os.Stdout, os.Stderr); err != nil {
			log.Fatalf("fakepi: %v", err)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}