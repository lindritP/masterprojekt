// Package conformance is the port conformance suite (SRS §7.4): the
// executable definition of the semantics every adapter must offer, and the
// operationalization of "materially comparable outcome in response to the
// same input" (Data Act Art. 2(37)).
//
// Each adapter package calls the suite for its port from a test that runs
// against a live backend, never a mock (AR-011). The suite only runs when
// CCSRA_CONFORMANCE=1, so `go test ./...` stays hermetic.
//
// Outcomes (AR-012): a passing test passes, a violated semantic fails, and an
// adapter returning ports.ErrUnsupported is reported as a skip whose message
// starts with "UNSUPPORTED:". `go test -json` makes all three machine-readable.
package conformance

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Enabled reports whether the live conformance suite should run.
func Enabled() bool { return os.Getenv("CCSRA_CONFORMANCE") == "1" }

// RequireEnabled skips the calling test unless the suite is enabled.
func RequireEnabled(t *testing.T) {
	t.Helper()
	if !Enabled() {
		t.Skip("set CCSRA_CONFORMANCE=1 and start the local stack (make stack-up) to run the port conformance suite")
	}
}

// skipIfUnsupported records an unsupported capability as a distinct outcome.
func skipIfUnsupported(t *testing.T, err error, capability string) {
	t.Helper()
	if errors.Is(err, ports.ErrUnsupported) {
		t.Skipf("UNSUPPORTED: %s: %v", capability, err)
	}
}

// RunID makes keys and payloads of one suite run unique.
func RunID() string { return fmt.Sprintf("run-%d-%d", os.Getpid(), nextSeq()) }

var seq int

func nextSeq() int { seq++; return seq }
