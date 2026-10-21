package cli

import (
	"bytes"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	d := Deps{Stdout: &out, Stderr: &bytes.Buffer{}, Version: "1.2.3"}
	if code := Main(d, []string{"version"}); code != 0 {
		t.Fatalf("Main(version) exit code = %d, want 0", code)
	}
	if got := out.String(); got != "yi 1.2.3\n" {
		t.Fatalf("Main(version) stdout = %q, want %q", got, "yi 1.2.3\n")
	}
}

func TestUnknownCommandExitCode(t *testing.T) {
	d := Deps{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Version: "1.2.3"}
	if code := Main(d, []string{"bogus"}); code != 2 {
		t.Fatalf("Main(bogus) exit code = %d, want 2", code)
	}
}
