package i18n_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestTheRootIsTheStandardLibraryPlusXText pins this package's dependency
// set, because the whole shape of the module rests on it.
//
// The lesson is lemmego/log's: with its provider inside the module, a
// consumer importing only the core inherited 289 modules for a package that
// formats text. Moving the provider out took it to 2.
//
// Here the specific temptation is api/app. A translator is read from a
// request, so declaring From(c app.Context) in this package reads as the
// obvious design — and api/app reaches api/db and therefore a SQL driver, so
// that one line would charge a worker rendering a mail subject for a Postgres
// driver. The second temptation is lemmego/inertia, which would charge the
// same worker for gonertia and cannot even be satisfied from inside api:
// inertia requires api, so that edge only goes one way.
//
// golang.org/x/text is the permitted exception, and only two trees of it:
// .../language for the matcher, and .../feature/plural for the CLDR rules.
func TestTheRootIsTheStandardLibraryPlusXText(t *testing.T) {
	for _, dep := range dependencies(t, ".") {
		// The allow-list runs first, and that ordering is itself a finding.
		// feature/plural imports message/catalog — Selectf lives in
		// feature/plural and takes catalog.Message — so the catalogue
		// machinery is in the closure whether or not anything here uses it.
		// It is the Printer that must stay out, not everything whose path
		// begins with "message".
		switch {
		case dep == "github.com/lemmego/i18n",
			strings.HasPrefix(dep, "github.com/lemmego/i18n/"),
			dep == "golang.org/x/text/language",
			strings.HasPrefix(dep, "golang.org/x/text/language/"),
			dep == "golang.org/x/text/feature/plural",
			// Permitted transitively rather than declared: the two trees above
			// reach these themselves.
			strings.HasPrefix(dep, "golang.org/x/text/internal"),
			strings.HasPrefix(dep, "golang.org/x/text/unicode"),
			strings.HasPrefix(dep, "golang.org/x/text/message/catalog"):
			continue
		}

		for _, denied := range deniedByName {
			if dep == denied {
				t.Errorf("the root package depends on %s.\n"+
					"That is the formatting this version deliberately does not ship — see "+
					"the README. If you are adding it on purpose, remove it from "+
					"deniedByName in the same commit, so the decision is reversed "+
					"rather than eroded.", dep)
			}
		}

		// A standard-library path has no dot in its first segment, which is
		// how the toolchain itself tells the two apart.
		first, _, _ := strings.Cut(dep, "/")
		if strings.Contains(first, ".") {
			t.Errorf("the root package depends on %s.\n"+
				"Only the standard library, golang.org/x/text/language and "+
				"golang.org/x/text/feature/plural belong here. If it is api/app, it "+
				"belongs in i18n/provider. If it is lemmego/inertia, it belongs in "+
				"i18n/i18ninertia — and it cannot live in api at all, because inertia "+
				"requires api.", dep)
		}
	}
}

// deniedByName are packages inside the one permitted tree, each encoding a
// scope decision rather than an accident.
//
// message and currency are how number and currency formatting would arrive,
// and this version ships neither: x/text's own documentation says message
// does not format time.Time, so a module claiming to localize everything
// would be wrong on the day it shipped, while the browser's
// Intl.DateTimeFormat is already correct and free. collate is sorting, which
// nobody has asked for and which carries its own tables.
var deniedByName = []string{
	"golang.org/x/text/message",
	"golang.org/x/text/number",
	"golang.org/x/text/currency",
	"golang.org/x/text/collate",
}

func dependencies(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("listing the dependencies of %s: %v", pkg, err)
	}
	return strings.Fields(string(out))
}

// TestTheProviderIsWhereTheApiDependencyLives asserts the split is real
// rather than intended, which is the other half of the guard above.
//
// Note that i18n/go.mod requires lemmego/api, and that is not a
// contradiction: the guard measures *packages*, and `go list -deps` resolves
// packages. A consumer importing github.com/lemmego/i18n links three
// non-standard packages whatever the module file says.
func TestTheProviderIsWhereTheApiDependencyLives(t *testing.T) {
	deps := dependencies(t, "./provider")
	if !slices.Contains(deps, "github.com/lemmego/api/app") {
		t.Error("i18n/provider does not depend on api/app, so either the provider is " +
			"not wired to the container or this test is in the wrong place")
	}

	// And the measurement that makes the split worth having.
	root := dependencies(t, ".")
	if len(root) >= len(deps) {
		t.Errorf("the root reaches %d packages and the provider %d; the whole point of "+
			"the split is that the root is the cheap one", len(root), len(deps))
	}
}

// TestTheDeniedListIsNotVacuous guards the guard.
//
// A denied entry that is spelled wrongly, or that names a package nothing
// could plausibly import, would make the test above pass while forbidding
// nothing — which is this project's most-repeated class of failing test. Each
// entry has to be a real importable package.
func TestTheDeniedListIsNotVacuous(t *testing.T) {
	for _, denied := range deniedByName {
		if err := exec.Command("go", "list", denied).Run(); err != nil {
			t.Errorf("%q is in deniedByName but is not a real package, so forbidding "+
				"it forbids nothing: %v", denied, err)
		}
	}
}

// TestOnlyI18nInertiaReachesInertia is the third guard, and the one with a
// module boundary behind it.
//
// lemmego/inertia requires lemmego/api, so the Inertia half of this feature
// could never have lived inside api — that edge only goes one way, which is
// why the provider is here rather than at api/providers/i18n. Having paid for
// the split, the cost has to stay where it was put: the root and the provider
// must not reach inertia or gonertia, so a worker rendering a mail subject
// links neither.
func TestOnlyI18nInertiaReachesInertia(t *testing.T) {
	for _, pkg := range []string{".", "./provider", "./i18ntest"} {
		for _, dep := range dependencies(t, pkg) {
			if dep == "github.com/lemmego/inertia" {
				t.Errorf("%s depends on %s. The shared page prop belongs in "+
					"i18n/i18ninertia; everything else here is read by code with no "+
					"browser anywhere near it.", pkg, dep)
			}
		}
	}

	// gonertia is denied for the root alone, and the asymmetry is a finding
	// about api rather than a concession here: api/app imports gonertia
	// directly, in app.go and context.go, so it is already among the core's
	// 257 packages and anything importing api/app inherits it. Denying it to
	// the provider would be denying api's own dependency. The root imports no
	// api, so there it is a real line.
	for _, dep := range dependencies(t, ".") {
		if strings.HasPrefix(dep, "github.com/romsar/gonertia") {
			t.Errorf("the root package depends on %s", dep)
		}
	}

	// And the positive half, so the guard cannot pass by the package having
	// quietly stopped doing its job.
	deps := dependencies(t, "./i18ninertia")
	for _, want := range []string{"github.com/lemmego/inertia", "github.com/romsar/gonertia/v3"} {
		if !slices.Contains(deps, want) {
			t.Errorf("i18ninertia does not depend on %s, so either it is not "+
				"wiring the prop or this test is in the wrong place", want)
		}
	}
}
