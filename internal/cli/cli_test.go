package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "barsdig "+Version) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "usage") {
		t.Fatalf("stderr = %q", errb.String())
	}
}
