package i18n

import (
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// Forms are the CLDR cardinal plural categories, which are the only form
// names a plural message may use.
//
// Named rather than positional, and that is the one place this design departs
// from Laravel on purpose. Laravel writes a plural as pipe-separated segments
// chosen by index, and its index comes from a switch whose own docblock says
// the rules are "derived from code of the Zend Framework (2010-09-25)" —
// pre-CLDR, matched by exact equality on the whole locale string, so "en-US"
// with a hyphen falls through to the default and returns the first segment.
// Then MessageSelector::choose ends:
//
//	if (count($segments) === 1 || ! isset($segments[$pluralIndex])) {
//	    return $segments[0];
//	}
//
// So Russian with two segments and a count of 5 computes index 2, finds
// nothing, and returns the singular. Silently, with no warning.
//
// Names fix that twice over. A translator opening ru/feed.json sees which
// forms their language needs, because the keys say so; and a form a
// translator did not write degrades to "other" — the plural — never to the
// singular. See selectForm.
var Forms = []string{"zero", "one", "two", "few", "many", "other"}

// formNames maps x/text's plural.Form to its CLDR name.
//
// Carried here because plural.Form has no String method and x/text's own
// name table is unexported, so there is no way to ask the library for these.
var formNames = map[plural.Form]string{
	plural.Other: "other",
	plural.Zero:  "zero",
	plural.One:   "one",
	plural.Two:   "two",
	plural.Few:   "few",
	plural.Many:  "many",
}

// validForm reports whether a key in a plural set is a form name or an exact
// count.
func validForm(name string) bool {
	if slices.Contains(Forms, name) {
		return true
	}
	count, ok := strings.CutPrefix(name, "=")
	if !ok || count == "" {
		return false
	}
	_, err := strconv.Atoi(count)
	return err == nil
}

// Form returns the CLDR cardinal form a locale uses for a count.
//
// Exported because it is the one piece of logic the browser half has to agree
// with, and a test that cannot call it cannot prove they agree. See
// testdata/plural_cases.json.
func Form(tag language.Tag, n int) string {
	if n < 0 {
		// CLDR plural rules read the absolute value: "-1 item" takes the same
		// form as "1 item".
		n = -n
	}
	// The six operands are i, v, w, f and t — the integer digits and four
	// descriptions of the visible fraction. All four fractional operands are
	// zero for an integer, and that is exactly why Choice takes an int: with
	// them zero, this and the browser's Intl.PluralRules.select(n) are reading
	// the same CLDR data with the same inputs, so the two cannot disagree. A
	// float count would expose the difference immediately, in Welsh and
	// Russian first.
	return formNames[plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0)]
}

// selectForm picks one string out of a plural set.
//
// Exact counts win over CLDR forms, which is ICU's rule and the useful half
// of Laravel's interval syntax: "no posts yet" is a different sentence from
// "0 posts", and no plural category can express that. Laravel's ranges
// ([1,19], [20,*]) are deliberately not supported — they overlap confusingly
// with the categories, and its own parser is looser than its documentation,
// accepting "{1]" while rejecting "[1, 19]" with a space.
func selectForm(forms map[string]string, tag language.Tag, n int) string {
	if text, ok := forms["="+strconv.Itoa(n)]; ok {
		return text
	}
	if text, ok := forms[Form(tag, n)]; ok {
		return text
	}
	if text, ok := forms["other"]; ok {
		return text
	}
	// A set with neither the locale's form nor "other" cannot happen through
	// the loader, which requires "other" — but a catalogue built in memory by
	// a test can produce one, and returning the first key in map order would
	// make that test flaky. Pick deterministically.
	for _, name := range Forms {
		if text, ok := forms[name]; ok {
			return text
		}
	}
	return ""
}
