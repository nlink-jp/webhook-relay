package cmd

import (
	"bytes"
	"testing"
)

// The tap formula's test runs `webhook-relay --version` and expects the
// version in the output; the server must answer it without any
// configuration, since the question is asked on a machine with no API key.
func TestVersionIsAnsweredBeforeConfiguration(t *testing.T) {
	t.Setenv("WEBHOOK_RELAY_API_KEY", "")
	for _, args := range [][]string{{"--version"}, {"-version"}, {"version"}} {
		var out bytes.Buffer
		if err := run("1.2.3", args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got, want := out.String(), "webhook-relay 1.2.3\n"; got != want {
			t.Fatalf("%v: got %q, want %q", args, got, want)
		}
	}
}

func TestOtherArgumentsAreNotTheVersion(t *testing.T) {
	for _, args := range [][]string{{}, {"--version", "x"}, {"serve"}, {"--help"}} {
		if versionRequested(args) {
			t.Fatalf("%v read as a version request", args)
		}
	}
}
