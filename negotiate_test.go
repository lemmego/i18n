package i18n_test

import (
	"testing"

	"github.com/lemmego/i18n"
)

func negotiator(t *testing.T, supported ...string) *i18n.Negotiator {
	t.Helper()
	neg, err := i18n.NewNegotiator(supported...)
	if err != nil {
		t.Fatal(err)
	}
	return neg
}

func TestAcceptLanguageIsNegotiatedByWeight(t *testing.T) {
	neg := negotiator(t, "en", "fr", "de")

	for _, tc := range []struct {
		header string
		want   string
	}{
		{"fr", "fr"},
		{"fr-CH, fr;q=0.9, en;q=0.8, de;q=0.7", "fr"},
		// The weights, not the order: German is written first and wanted
		// least. ParseAcceptLanguage sorts by weight, which is why this
		// package does not read ";q=" itself.
		{"de;q=0.2, en;q=0.9", "en"},
		{"en-GB", "en"},
	} {
		res, ok := neg.FromHeader(tc.header)
		if !ok {
			t.Errorf("FromHeader(%q) matched nothing", tc.header)
			continue
		}
		if res.Locale != tc.want {
			t.Errorf("FromHeader(%q) = %q, want %q", tc.header, res.Locale, tc.want)
		}
	}
}

// TestAnUnsupportedHeaderMatchesNothing is what lets the middleware try its
// next source instead of stopping at a value that was never honoured.
func TestAnUnsupportedHeaderMatchesNothing(t *testing.T) {
	neg := negotiator(t, "en", "fr")
	if res, ok := neg.FromHeader("ja, ko;q=0.8"); ok {
		t.Errorf("FromHeader matched %q for a header naming neither supported locale; "+
			"reporting false is what lets the caller fall through to its next source",
			res.Locale)
	}
	if _, ok := neg.FromHeader(""); ok {
		t.Error("an empty header matched")
	}
	// "Accept-Language: *" says "any language will do", so it names no
	// preference to honour and the caller falls through to its next source and
	// then to the default — which is the same locale a reader asking for
	// anything would have been served anyway.
	if res, ok := neg.FromHeader("*"); ok {
		t.Errorf("a wildcard header matched %q; it expresses no preference, so there "+
			"is nothing for it to match", res.Locale)
	}
}

func TestLookupMatchesASingleValue(t *testing.T) {
	neg := negotiator(t, "en", "pt-BR")

	for _, tc := range []struct {
		value string
		want  string
		ok    bool
	}{
		{"pt-BR", "pt-BR", true},
		{"pt", "pt-BR", true},
		{"en", "en", true},
		{"ja", "", false},
		{"", "", false},
		{"not a tag at all", "", false},
	} {
		res, ok := neg.Lookup(tc.value)
		if ok != tc.ok {
			t.Errorf("Lookup(%q) ok = %v, want %v", tc.value, ok, tc.ok)
			continue
		}
		if ok && res.Locale != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.value, res.Locale, tc.want)
		}
	}
}

func TestTheFirstSupportedLocaleIsTheDefault(t *testing.T) {
	neg := negotiator(t, "fr", "en")
	if got := neg.Default().Locale; got != "fr" {
		t.Errorf("default = %q, want fr; x/text defines the first tag given to a "+
			"matcher as its fallback, so the default has to be first rather than "+
			"named separately", got)
	}
	if got := neg.Default().Source; got != i18n.SourceDefault {
		t.Errorf("source = %v, want default", got)
	}
}

// TestTheIndexDecidesTheLocaleNotTheTag pins the subtle half of x/text's
// matcher: Match may return a tag that is not one of the supported ones,
// because it carries over settings from what the reader asked for. The tag is
// right for initialising a formatter and wrong for looking up a catalogue.
func TestTheIndexDecidesTheLocaleNotTheTag(t *testing.T) {
	neg := negotiator(t, "en", "de")
	res, ok := neg.Lookup("de-DE-u-co-phonebk")
	if !ok {
		t.Fatal("a German tag with a collation extension matched nothing")
	}
	if res.Locale != "de" {
		t.Errorf("locale = %q, want de — the catalogue is keyed by the supported "+
			"locale, not by whatever the reader's tag carried", res.Locale)
	}
}

func TestLocalesAreReturnedDefaultFirst(t *testing.T) {
	neg := negotiator(t, "fr", "en", "de")
	got := neg.Locales()
	if len(got) != 3 || got[0] != "fr" {
		t.Errorf("Locales() = %v, want fr first", got)
	}
	// A copy, so a caller cannot reorder the matcher's own list underneath it.
	got[0] = "xx"
	if neg.Locales()[0] != "fr" {
		t.Error("Locales() handed out the internal slice")
	}
}
