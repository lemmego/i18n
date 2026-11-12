package i18n

import (
	"fmt"
	"slices"

	"golang.org/x/text/language"
)

// Source says where a request's locale came from. It exists so a dashboard,
// a log line or a test can tell "this reader chose French" from "we guessed
// French from a browser header".
type Source uint8

const (
	SourceDefault  Source = iota // nothing said otherwise
	SourceHeader                 // Accept-Language
	SourceCookie                 // the locale cookie
	SourceQuery                  // ?lang=
	SourceUser                   // the signed-in reader's stored preference
	SourceExplicit               // a handler called Prefer
)

func (s Source) String() string {
	switch s {
	case SourceHeader:
		return "header"
	case SourceCookie:
		return "cookie"
	case SourceQuery:
		return "query"
	case SourceUser:
		return "user"
	case SourceExplicit:
		return "explicit"
	default:
		return "default"
	}
}

// Resolution is a settled locale: which one, where it came from, and the
// parsed tag the plural rules need.
type Resolution struct {
	Tag    language.Tag
	Locale string
	Index  int
	Source Source
}

// Negotiator matches what a reader asked for against what an application
// supports.
type Negotiator struct {
	locales []string
	tags    []language.Tag
	byName  map[string]int
	matcher language.Matcher
}

// NewNegotiator returns a negotiator over the supported locales, the first of
// which is the default.
//
// Each is parsed and then compared against its own canonical spelling, and a
// mismatch is an error naming the canonical form. That check is the reason
// "pt-br" cannot reach production: a catalogue directory is looked up by this
// string, macOS is case-insensitive and Linux is not, so without it the
// locale works on every laptop and silently serves the default in the image.
func NewNegotiator(supported ...string) (*Negotiator, error) {
	if len(supported) == 0 {
		return nil, fmt.Errorf("i18n: no supported locales; the first is the default, so there must be at least one")
	}

	n := &Negotiator{byName: make(map[string]int, len(supported))}
	for _, locale := range supported {
		tag, err := language.Parse(locale)
		if err != nil {
			return nil, fmt.Errorf("i18n: %q is not a language tag: %w", locale, err)
		}
		if canonical := tag.String(); canonical != locale {
			return nil, fmt.Errorf("i18n: the supported locale %q is spelled %q canonically; "+
				"a catalogue directory is found by this name, and a case-insensitive "+
				"filesystem would hide the difference until the Linux image", locale, canonical)
		}
		if _, dup := n.byName[locale]; dup {
			return nil, fmt.Errorf("i18n: the locale %q is listed twice", locale)
		}
		n.byName[locale] = len(n.tags)
		n.tags = append(n.tags, tag)
		n.locales = append(n.locales, locale)
	}

	// x/text defines the first tag given to a matcher as the fallback, which
	// is why the default locale has to be first rather than named separately.
	n.matcher = language.NewMatcher(n.tags)
	return n, nil
}

// Locales returns the supported locales, the default first.
func (n *Negotiator) Locales() []string { return slices.Clone(n.locales) }

// Default returns the resolution for the default locale.
func (n *Negotiator) Default() Resolution {
	return Resolution{Tag: n.tags[0], Locale: n.locales[0], Index: 0, Source: SourceDefault}
}

// tagOf returns the parsed tag for a supported locale.
func (n *Negotiator) tagOf(locale string) language.Tag {
	if i, ok := n.byName[locale]; ok {
		return n.tags[i]
	}
	return n.tags[0]
}

// Lookup matches one value — a cookie, a stored preference, a query
// parameter — against the supported locales.
//
// It reports false rather than quietly returning the default when nothing
// matches, so the caller can try its next source instead of stopping at a
// value that was never honoured.
func (n *Negotiator) Lookup(locale string) (Resolution, bool) {
	if locale == "" {
		return Resolution{}, false
	}
	tag, err := language.Parse(locale)
	if err != nil {
		// Parse returns a usable tag alongside an error for a well-formed tag
		// with an unknown subtag, so an ill-formed one is the only real
		// refusal here.
		if tag == language.Und {
			return Resolution{}, false
		}
	}
	return n.match(tag)
}

// FromHeader matches an Accept-Language header.
//
// ParseAcceptLanguage handles the quality values, returning the tags already
// ordered by weight with the zero-weighted ones dropped, so this does not
// have to read ";q=" itself.
func (n *Negotiator) FromHeader(accept string) (Resolution, bool) {
	if accept == "" {
		return Resolution{}, false
	}
	tags, _, err := language.ParseAcceptLanguage(accept)
	if err != nil && len(tags) == 0 {
		return Resolution{}, false
	}
	return n.match(tags...)
}

// match runs the x/text matcher and reads the index rather than the tag.
//
// The index, and this is not a detail: Match may return a tag that is not one
// of the supported ones, because it carries over settings — a -u extension,
// say — from what the reader asked for. The tag is right for initializing a
// formatter and wrong for looking up a catalogue, so the catalogue is keyed
// by the index.
func (n *Negotiator) match(tags ...language.Tag) (Resolution, bool) {
	if len(tags) == 0 {
		return Resolution{}, false
	}
	_, index, confidence := n.matcher.Match(tags...)
	if confidence == language.No {
		return Resolution{}, false
	}
	return Resolution{Tag: n.tags[index], Locale: n.locales[index], Index: index}, true
}

// rtlScripts are the right-to-left scripts an application is plausibly
// localized into. Checked by script rather than by language so that, for
// instance, Kurdish in Arabic script is right-to-left and Kurdish in Latin
// script is not, without either being listed.
var rtlScripts = map[string]bool{
	"Arab": true, "Hebr": true, "Thaa": true, "Syrc": true, "Nkoo": true,
	"Samr": true, "Mand": true, "Adlm": true, "Rohg": true, "Yezi": true,
}

// Dir returns "rtl" or "ltr" for a tag.
//
// Computed in Go and carried to the browser as a value, never guessed in
// JavaScript from a locale prefix, so there is one answer and one place it
// comes from.
func Dir(tag language.Tag) string {
	script, _ := tag.Script()
	if rtlScripts[script.String()] {
		return "rtl"
	}
	return "ltr"
}
