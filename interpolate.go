package i18n

import (
	"fmt"
	"slices"
	"strings"
)

// interpolate substitutes :name placeholders in a message.
//
// Two properties are load-bearing and neither is the obvious reading of the
// code, so both are stated here and pinned by tests.
//
// The pairs are sorted longest-key-first because [strings.NewReplacer]
// prefers the first pair in argument order that matches at a position, where
// PHP's strtr — which this syntax comes from — prefers the longest. Unsorted,
// with "value" handed over before "values", the message "one of :values"
// renders as "one of <the value>s". Both placeholders are in this
// framework's own validation catalogue: Contains uses :value and In uses
// :values.
//
// And the replacer never rescans its own output, which is what makes a
// replacement value containing a colon safe. That matters in exactly one
// place today: the multi-file upload message substitutes a client-supplied
// filename, so a file named ":message.txt" has to stay literal. A loop of
// strings.ReplaceAll would be wrong on both counts.
//
// An unknown placeholder is left verbatim rather than blanked. ":nmae" in the
// page is a bug report; an empty space is a mystery.
func interpolate(text string, repl R) string {
	if len(repl) == 0 || !strings.ContainsRune(text, ':') {
		return text
	}

	keys := make([]string, 0, len(repl))
	for key := range repl {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if d := len(b) - len(a); d != 0 {
			return d
		}
		// Ties broken by name so the replacer is built identically every
		// time; Go's map order is random and a non-deterministic replacer
		// would make a failure reproduce only sometimes.
		return strings.Compare(a, b)
	})

	pairs := make([]string, 0, len(repl)*2)
	for _, key := range keys {
		pairs = append(pairs, ":"+key, display(repl[key]))
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// display renders a replacement value.
//
// A []string joins with ", " rather than going through fmt's "[a b c]",
// because these values are read by people: the In rule lists the values a
// field may take, and Go's slice syntax in the middle of a sentence is a
// defect.
func display(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []string:
		return strings.Join(v, ", ")
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
