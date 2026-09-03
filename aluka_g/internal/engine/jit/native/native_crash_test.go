//go:build (windows || linux) && amd64

package native

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

const nativeCrashHelperEnv = "ALUKA_NATIVE_CRASH_HELPER"

func TestNativeCrashIsIsolated(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeCrashIsolationHelper$")
	cmd.Env = append(os.Environ(), nativeCrashHelperEnv+"=1", "GOTRACEBACK=none")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid native code unexpectedly returned successfully: %s", output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.Success() {
		t.Fatalf("invalid native helper did not exit as a failed subprocess: err=%v output=%s", err, output)
	}
}

func TestNativeCrashIsolationHelper(t *testing.T) {
	if os.Getenv(nativeCrashHelperEnv) != "1" {
		return
	}
	// UD2 is guaranteed to raise an invalid-opcode exception on amd64. The
	// parent test verifies that this failure is contained in this process.
	code, err := Publish([]byte{0x0f, 0x0b})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	code.Call(&Frame{})
	t.Fatal("UD2 unexpectedly returned")
}
