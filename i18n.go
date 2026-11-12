// Package i18n translates an application's messages.
//
// A [Catalog] is built once at boot from JSON files, and a [Translator] reads
// one locale out of it:
//
//	cat, err := i18n.LoadDir("resources/lang", "en", "fr")
//	t := cat.In("fr")
//	t.Get("auth.failed")              // "Ces identifiants ne correspondent pas."
//	t.Get("mail.hello", i18n.R{"name": "Ada"})
//	t.Choice("feed.posts", 3)         // "3 publications" — :count is bound for you
//
// # Why this module depends on almost nothing
//
// The root package imports the standard library and two trees of
// golang.org/x/text — language, for parsing tags and negotiating against an
// Accept-Language header, and feature/plural, for the CLDR plural rules. It
// imports nothing else, and deps_test.go fails if that changes.
//
// The reason is that a translator is read from places that are not a web
// request. A worker renders a mail subject; a CLI command prints a message; a
// test wants two strings and no application. Declaring From(c app.Context)
// here would read as the obvious design and would charge every one of those
// for api/app — which reaches api/db and therefore a SQL driver. So the
// app.Provider lives in i18n/provider, and the Inertia prop in
// i18n/i18ninertia, which cannot live in api at all: lemmego/inertia requires
// lemmego/api, so that edge only goes one way.
//
// The same rule keeps number, currency and date formatting out. x/text's own
// documentation says message does not format time.Time, so a module claiming
// to localize everything would be wrong on the day it shipped, while the
// browser's Intl.DateTimeFormat is already correct and free. Those packages
// are denied by name in deps_test.go rather than merely unused, so reversing
// the decision takes a deliberate commit.
package i18n

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"golang.org/x/text/language"
)

// R is a shorthand for a replacement set, so a call site reads as prose
// rather than as a type name:
//
//	t.Get("mail.greeting", i18n.R{"name": user.Name})
type R = map[string]any

// message is one catalogue entry: either a plain string, or a set of plural
// forms keyed by a CLDR form name ("one", "other") or an exact count ("=0").
//
// The two are distinguished at load time rather than at lookup time, because
// a JSON object is ambiguous between a plural set and a nesting level and
// guessing is how one message quietly disappears in one language. See
// readGroup.
type message struct {
	text  string
	forms map[string]string
}

func (m message) plural() bool { return m.forms != nil }

// Catalog holds every message of every locale an application supports.
//
// One Catalog is built at boot and read by every request thereafter, so it is
// immutable once loading is finished and safe for concurrent use. The single
// mutable thing on it is the record of keys that were asked for and missing,
// which is guarded by a mutex and exists so an untranslated page reports
// itself.
type Catalog struct {
	neg *Negotiator

	// app holds the application's own messages, keyed locale then key. A key
	// may be namespaced ("authz::errors.forbidden"), which is how an
	// application overrides a message that a module shipped.
	app map[string]map[string]message

	// mounted holds the defaults modules shipped, in the same shape. It is
	// consulted only after app, so load order does not matter: an override
	// published before or after the module mounted its catalogue wins either
	// way. Keeping them in two maps rather than merging on load is what makes
	// that true.
	mounted map[string]map[string]message

	// builtin is the framework's own English, the floor under every locale.
	//
	// A third tier rather than a locale in the map above, because it is the
	// answer for a key nobody has translated in any locale — which is what
	// makes validation read in English in a project that has published no
	// catalogue at all. Laravel's own package language files play exactly this
	// role, with the application's lang/ directory overriding them.
	builtin map[string]message

	mu     sync.Mutex
	missed map[string]int
}

// NewCatalog returns an empty catalogue over the given supported locales.
//
// The first locale is the default, and is also the fallback for a key a later
// locale does not define — the same convention x/text's matcher uses for the
// first tag it is given, so there is one rule rather than two.
func NewCatalog(supported ...string) (*Catalog, error) {
	neg, err := NewNegotiator(supported...)
	if err != nil {
		return nil, err
	}
	return &Catalog{
		neg:     neg,
		app:     map[string]map[string]message{},
		mounted: map[string]map[string]message{},
		builtin: builtinMessages(),
		missed:  map[string]int{},
	}, nil
}

// Negotiator returns the catalogue's locale negotiator.
func (c *Catalog) Negotiator() *Negotiator { return c.neg }

// Locales returns the supported locales, the default first.
func (c *Catalog) Locales() []string { return slices.Clone(c.neg.locales) }

// Default returns the default locale.
func (c *Catalog) Default() string { return c.neg.locales[0] }

// In returns a translator fixed to one locale.
//
// For a worker, a CLI command or a test — anywhere the locale is already
// known and will not change. A request uses [Catalog.Deferred] instead.
//
// An unsupported locale is matched to the nearest supported one rather than
// refused, so a stale value in a database column degrades to a readable page.
func (c *Catalog) In(locale string) *Translator {
	res, ok := c.neg.Lookup(locale)
	if !ok {
		res = c.neg.Default()
	}
	return &Translator{r: &request{cat: c, res: res, frozen: true}}
}

// AddMessages adds messages for one locale, keyed by full dotted key.
//
// The same shape a catalogue file holds, one level flatter: a value is either
// the message or a map of plural forms.
//
//	cat.AddMessages("en", map[string]any{
//	    "feed.empty": "Nothing here yet",
//	    "feed.posts": map[string]string{"one": ":count post", "other": ":count posts"},
//	})
//
// This is Laravel's addLines, and it is what a test uses instead of a
// directory of files. It is also the seam for a module that would rather
// register its strings in Go than embed a filesystem.
//
// A plural set is checked here exactly as the loader checks one, so a message
// added this way cannot be a shape a message read from a file would have been
// refused for.
func (c *Catalog) AddMessages(locale string, messages map[string]any) error {
	if _, ok := c.neg.byName[locale]; !ok {
		return fmt.Errorf("i18n: %q is not a supported locale; this application supports %s",
			locale, display(c.Locales()))
	}
	for key, value := range messages {
		switch v := value.(type) {
		case string:
			c.put(c.app, locale, key, message{text: v})
		case map[string]string:
			if err := checkForms(key, v); err != nil {
				return err
			}
			forms := make(map[string]string, len(v))
			maps.Copy(forms, v)
			c.put(c.app, locale, key, message{forms: forms})
		default:
			return fmt.Errorf("i18n: %s is %T; a message is a string, or a "+
				"map[string]string of plural forms", key, value)
		}
	}
	return nil
}

// checkForms applies the loader's rules to a plural set built in memory.
func checkForms(key string, forms map[string]string) error {
	if len(forms) == 0 {
		return fmt.Errorf("i18n: the plural message %s has no forms", key)
	}
	for name := range forms {
		if !validForm(name) {
			return fmt.Errorf("i18n: %s has the plural form %q, which is neither a CLDR "+
				"category (%s) nor an exact count like \"=0\"", key, name, display(Forms))
		}
	}
	if _, ok := forms["other"]; !ok {
		return fmt.Errorf("i18n: the plural message %s has no \"other\" form. Every "+
			"language uses it, and it is what a form this language does not list "+
			"falls back to", key)
	}
	return nil
}

// put stores a message, used by the loaders.
func (c *Catalog) put(into map[string]map[string]message, locale, key string, m message) {
	byKey, ok := into[locale]
	if !ok {
		byKey = map[string]message{}
		into[locale] = byKey
	}
	byKey[key] = m
}

// lookup finds a key in one locale, the application's own messages first.
func (c *Catalog) lookup(locale, key string) (message, bool) {
	if m, ok := c.app[locale][key]; ok {
		return m, true
	}
	m, ok := c.mounted[locale][key]
	return m, ok
}

// find walks a locale and then the default, which is the whole fallback
// chain.
//
// Two entries, deliberately, and not a walk up from "pt-BR" to "pt": a parent
// locale is a guess about which messages are shared, and a project that wants
// it lists both and copies the file. Laravel's chain is the same two entries
// for the same reason.
func (c *Catalog) find(locale, key string) (message, language.Tag, bool) {
	if m, ok := c.lookup(locale, key); ok {
		return m, c.neg.tagOf(locale), true
	}
	if def := c.Default(); def != locale {
		if m, ok := c.lookup(def, key); ok {
			return m, c.neg.tagOf(def), true
		}
	}
	if m, ok := c.builtin[key]; ok {
		// The framework's own English, which is why a project that registers
		// the provider and publishes nothing still reads its validation
		// errors as sentences.
		//
		// The tag returned is English and not the application's default, and
		// that pairing matters: the plural rules have to match the language of
		// the text they are selecting from, or a Russian application falls
		// back to an English sentence and then asks Russian rules which of
		// its four forms to use.
		return m, language.English, true
	}
	c.miss(key)
	return message{}, language.Und, false
}

// miss records that a key was asked for and not found.
//
// Counted rather than returned as an error, for the reason a missing string
// is not worth a blank page: the reader gets the key, which is legible and
// obviously wrong, and the count is what a test or a dashboard reads to find
// out before a reader does.
func (c *Catalog) miss(key string) {
	c.mu.Lock()
	c.missed[key]++
	c.mu.Unlock()
}

// Misses returns the keys that were asked for and not found, with how many
// times each was asked for.
func (c *Catalog) Misses() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.missed)
}

// ResetMisses clears the record, for a test that wants one page's worth.
func (c *Catalog) ResetMisses() {
	c.mu.Lock()
	c.missed = map[string]int{}
	c.mu.Unlock()
}

// Diff returns the keys that locale a defines and locale b does not, sorted.
//
// Exported rather than enforced at load time, because whether an unfinished
// translation should block a deploy is the application's call: a project
// shipping a locale a translator is still working through wants the warning,
// not a failed boot. A project that does want it writes the one-line test.
func (c *Catalog) Diff(a, b string) []string {
	var missing []string
	for _, source := range []map[string]map[string]message{c.app, c.mounted} {
		for key := range source[a] {
			if _, ok := c.lookup(b, key); !ok {
				missing = append(missing, key)
			}
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing)
}

// Keys returns every key defined for a locale, sorted.
func (c *Catalog) Keys(locale string) []string {
	var keys []string
	keys = append(keys, slices.Collect(maps.Keys(c.app[locale]))...)
	keys = append(keys, slices.Collect(maps.Keys(c.mounted[locale]))...)
	slices.Sort(keys)
	return slices.Compact(keys)
}

// Export returns a locale's messages in the shape the browser half reads:
// flat dotted keys, a plain message as a string, a plural set as an object.
//
// Flat because the Go key space is dotted, so there is one key format in the
// system and the JavaScript side is a map lookup rather than a path walk.
func (c *Catalog) Export(locale string) map[string]any {
	out := map[string]any{}
	add := func(key string, m message) {
		if m.plural() {
			out[key] = maps.Clone(m.forms)
			return
		}
		out[key] = m.text
	}

	// Lowest tier first, so the later writes override: the framework's own
	// English, then the default locale, then the requested one.
	//
	// The floor and the default both travel, because the browser cannot fall
	// back to a catalogue it was never sent. That is the one way this payload
	// differs from the server's lookup, where the tiers are consulted in turn
	// instead.
	for key, m := range c.builtin {
		add(key, m)
	}
	for _, l := range []string{c.Default(), locale} {
		for _, source := range []map[string]map[string]message{c.mounted, c.app} {
			for key, m := range source[l] {
				add(key, m)
			}
		}
	}
	return out
}

// render resolves a key against a locale and substitutes placeholders.
func (c *Catalog) render(locale, key string, n int, choose bool, repl R) string {
	m, tag, ok := c.find(locale, key)
	if !ok {
		// Laravel's behaviour, and the right one: the key is legible, obviously
		// wrong, and quick to spot. Placeholders are still substituted, so a
		// missing plural key shows the count it was called with.
		return interpolate(key, repl)
	}

	text := m.text
	if m.plural() {
		text = selectForm(m.forms, tag, n)
	} else if choose {
		// A plural call against a plain message is a catalogue mistake rather
		// than a caller mistake — the caller cannot know which form a
		// translator used — so it renders the string it found instead of
		// failing, and the count is still available as :count.
		text = m.text
	}
	return interpolate(text, repl)
}

func errAt(path, format string, args ...any) error {
	return fmt.Errorf("i18n: %s: %s", path, fmt.Sprintf(format, args...))
}
