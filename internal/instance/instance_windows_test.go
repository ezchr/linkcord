package instance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// helperEnv makes the test binary act as a second Linkcord: it tries to
// take the lock named by the variable and exits with 3 if it cannot.
const helperEnv = "LINKCORD_INSTANCE_HELPER"

func TestMain(m *testing.M) {
	if name := os.Getenv(helperEnv); name != "" {
		mutexName, focusClass, focusTries = name, "LinkcordInstanceTestNoWindow", 1
		if !Acquire() {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// second runs the test binary as a second Linkcord and reports whether it
// was allowed to run.
func second(t *testing.T, name string) bool {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+name)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true
	case errors.As(err, &exit) && exit.ExitCode() == 3:
		return false
	default:
		t.Fatalf("second instance: %v", err)
		return false
	}
}

func TestOneInstance(t *testing.T) {
	name := fmt.Sprintf(`Local\Linkcord.Test.%d`, os.Getpid())

	// Nobody holds the lock yet.
	if !second(t, name) {
		t.Fatal("a Linkcord started while none ran was told another one runs")
	}

	mutexName, focusClass, focusTries = name, "LinkcordInstanceTestNoWindow", 1
	if !Acquire() {
		t.Fatal("the first Linkcord could not take the lock")
	}
	if second(t, name) {
		t.Fatal("a second Linkcord started while the first ran")
	}
}
