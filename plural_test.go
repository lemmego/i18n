package i18n_test

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/lemmego/i18n"
)

// pluralFixture mirrors testdata/plural_cases.json, which is read by this
// test and by js/i18n.test.ts.
//
// One file, two readers, deliberately: the Go side selects a plural form from
// x/text's CLDR tables and the browser side from Intl.PluralRules, and the
// only thing keeping those two answers equal is that both are checked against
// the same expectations. A divergence is then a red test rather than a bug
// report written in Arabic.
type pluralFixture struct {
	Why    string            `json:"why"`
	Locale string            `json:"locale"`
	Forms  map[string]string `json:"forms"`
	Cases  []struct {
		N    int    `json:"n"`
		Want string `json:"want"`
	} `json:"cases"`
}

func readPluralFixtures(t *testing.T) []pluralFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/plural_cases.json")
	if err != nil {
		t.Fatalf("reading the shared fixture: %v", err)
	}
	var fixtures []pluralFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("parsing the shared fixture: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("the shared fixture is empty, so this test proves nothing")
	}
	return fixtures
}

func TestPluralsMatchTheSharedFixture(t *testing.T) {
	for _, fixture := range readPluralFixtures(t) {
		t.Run(fixture.Locale+"/"+fixture.Why, func(t *testing.T) {
			cat := catalogWith(t, fixture.Locale, map[string]any{
				"items": fixture.Forms,
			})
			translator := cat.In(fixture.Locale)

			for _, want := range fixture.Cases {
				got := translator.Choice("test.items", want.N)
				if got != want.Want {
					t.Errorf("Choice(%d) = %q, want %q", want.N, got, want.Want)
				}
			}
		})
	}
}

// TestAnUnwrittenFormDegradesToTheGeneralOne is the Laravel bug, asserted as
// not happening.
//
// Laravel picks a plural segment by index and ends:
//
//	if (count($segments) === 1 || ! isset($segments[$pluralIndex])) {
//	    return $segments[0];
//	}
//
// so Russian with two segments and five apples computes index 2, finds
// nothing, and returns segment zero — the singular. Named forms cannot do
// that, because there is no index to fall off the end of.
func TestAnUnwrittenFormDegradesToTheGeneralOne(t *testing.T) {
	cat := catalogWith(t, "ru", map[string]any{
		"apples": map[string]any{"one": "одно яблоко", "other": "много яблок"},
	})

	if got := cat.In("ru").Choice("test.apples", 5); got != "много яблок" {
		t.Errorf("five apples in Russian rendered %q; a form the translator did not "+
			"write must fall back to the plural, never to the singular", got)
	}
}

func TestFormReportsTheCldrCategory(t *testing.T) {
	cat, err := i18n.NewCatalog("en", "ru")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		locale string
		n      int
		want   string
	}{
		{"en", 1, "one"},
		{"en", 2, "other"},
		{"ru", 1, "one"},
		{"ru", 3, "few"},
		{"ru", 5, "many"},
	} {
		tag := cat.In(tc.locale).Tag()
		if got := i18n.Form(tag, tc.n); got != tc.want {
			t.Errorf("Form(%s, %d) = %q, want %q", tc.locale, tc.n, got, tc.want)
		}
	}
}

// TestEveryFormNameIsOneTheBrowserCanProduce constrains what the Go side may
// be handed, so Intl.PluralRules can never be asked for a category it does
// not generate. The loader refuses these too; this is the same rule asserted
// against the framework's own shipped catalogue.
func TestEveryFormNameIsOneTheBrowserCanProduce(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	exported := cat.Export("en")
	if len(exported) == 0 {
		t.Fatal("the framework's own catalogue exported nothing")
	}

	for key, value := range exported {
		forms, ok := value.(map[string]string)
		if !ok {
			continue
		}
		for name := range forms {
			if slicesHas(i18n.Forms, name) {
				continue
			}
			if count, isExact := trimExact(name); isExact {
				if _, err := strconv.Atoi(count); err == nil {
					continue
				}
			}
			t.Errorf("%s has the plural form %q, which is neither a CLDR category "+
				"(%v) nor an exact count like \"=0\"", key, name, i18n.Forms)
		}
	}
}

func slicesHas(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func trimExact(name string) (string, bool) {
	if len(name) > 1 && name[0] == '=' {
		return name[1:], true
	}
	return "", false
}
