package i18n_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTheBrowserHalfAgreesWithGo runs js/i18n.test.mjs, which reads the same
// testdata/plural_cases.json this package's Go test reads.
//
// It is driven from `go test` on purpose. The two halves of this module have
// to produce the same sentence for the same message, and the only thing that
// makes that true is a test somebody actually runs — so it runs in the one
// command the module's CI already invokes, rather than in a JavaScript test
// setup this repository does not have.
//
// Skipped rather than failed when Node is absent, because Go developers
// working on the catalogue should not need it installed. CI has it, which is
// where the assertion has to hold.
func TestTheBrowserHalfAgreesWithGo(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed, so the browser half is unchecked here; CI has it")
	}

	out, err := exec.Command(node, "--test", "js/i18n.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("the browser half disagrees with the shared fixture, or its own "+
			"tests failed:\n%s", out)
	}
	// A test runner that matched no files exits zero, which would make this
	// pass while running nothing — the failure this project keeps finding.
	if !strings.Contains(string(out), "# pass ") || strings.Contains(string(out), "# pass 0") {
		t.Fatalf("node ran but reported no passing tests, so this assertion is "+
			"vacuous:\n%s", out)
	}
}
