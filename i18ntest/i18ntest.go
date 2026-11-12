// Package i18ntest holds the test helpers for lemmego/i18n.
//
// A test that wants two strings should not need a resources/lang directory,
// and a test that wants to know whether a page is fully translated should not
// have to read its HTML. Those are the two things here.
package i18ntest

import (
	"testing"

	"github.com/lemmego/i18n"
)

// Catalog returns a catalogue holding exactly the messages given, for one
// locale which is also the default.
//
// Values are strings or map[string]string plural sets, keyed by full dotted
// key:
//
//	cat := i18ntest.Catalog(t, "en", map[string]any{
//	    "feed.posts": map[string]string{"one": ":count post", "other": ":count posts"},
//	})
//
// The framework's own English is still underneath, so validation.required
// resolves whether or not a test mentions it. That matches what an
// application gets, which is the point of a harness.
func Catalog(t testing.TB, locale string, messages map[string]any) *i18n.Catalog {
	t.Helper()
	cat, err := i18n.NewCatalog(locale)
	if err != nil {
		t.Fatalf("i18ntest: %v", err)
	}
	if err := cat.AddMessages(locale, messages); err != nil {
		t.Fatalf("i18ntest: %v", err)
	}
	return cat
}

// Translator returns a translator over [Catalog]'s result.
func Translator(t testing.TB, locale string, messages map[string]any) *i18n.Translator {
	t.Helper()
	return Catalog(t, locale, messages).In(locale)
}

// Multi returns a catalogue over several locales, the first being the
// default, so a test can exercise the fallback between them.
func Multi(t testing.TB, locales []string, messages map[string]map[string]any) *i18n.Catalog {
	t.Helper()
	cat, err := i18n.NewCatalog(locales...)
	if err != nil {
		t.Fatalf("i18ntest: %v", err)
	}
	for locale, set := range messages {
		if err := cat.AddMessages(locale, set); err != nil {
			t.Fatalf("i18ntest: %v", err)
		}
	}
	return cat
}

// AssertNoMisses fails if the catalogue was asked for a key it does not have.
//
// This is how an application asserts that a page is fully translated without
// reading its markup: render the page, then call this. A substring check
// against HTML cannot tell a missing key from a key that legitimately appears
// in the copy, and this project has shipped exactly that mistake before — a
// leak test that passed because the page echoed the query back.
//
// Reset the catalogue between pages with ResetMisses if one test renders
// several.
func AssertNoMisses(t testing.TB, cat *i18n.Catalog) {
	t.Helper()
	misses := cat.Misses()
	if len(misses) == 0 {
		return
	}
	for key, count := range misses {
		t.Errorf("the key %q was asked for %d time(s) and is not in the catalogue, so "+
			"the reader saw the key instead of a sentence", key, count)
	}
}

// AssertComplete fails if a locale is missing any key the default locale
// defines, naming each one.
//
// Not enforced at load time, because whether an unfinished translation should
// block a deploy is the application's decision — a project shipping a locale
// a translator is still working through wants the list, not a failed boot.
// This is the one line that turns it into a gate for a project that does want
// one.
func AssertComplete(t testing.TB, cat *i18n.Catalog, locale string) {
	t.Helper()
	missing := cat.Diff(cat.Default(), locale)
	for _, key := range missing {
		t.Errorf("%s has no translation for %q", locale, key)
	}
}
