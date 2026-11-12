package i18n

import (
	"errors"
	"sync"

	"golang.org/x/text/language"
)

// ErrLocaleAlreadyUsed is returned by [Translator.Prefer] when the request
// has already translated something and the locale is therefore settled.
var ErrLocaleAlreadyUsed = errors.New("i18n: this request has already been translated, so its locale is settled")

// Translator reads one locale's messages.
//
// A translator taken from a request resolves its locale on first use rather
// than on construction — see [Catalog.Deferred] — so several translators
// taken from the same request share one answer. Safe for concurrent use,
// which is not decoration: the Inertia renderer resolves every page prop in
// its own goroutine.
type Translator struct{ r *request }

// request is the mutable half, held by pointer so that a late source can
// still win and so every Translator taken from this request sees one answer.
type request struct {
	mu     sync.Mutex
	cat    *Catalog
	res    Resolution
	late   func() (string, bool)
	frozen bool
}

// Deferred returns a translator whose locale is settled on first use.
//
// initial is what the request's cheap sources — a cookie, a query parameter,
// an Accept-Language header — already established. late is consulted once, on
// the first translation, and is where a signed-in reader's stored preference
// goes.
//
// Lazy, and that is the whole trick. The negotiating middleware wraps the
// mux, so it runs before any route's own authentication handler and there is
// no reader to ask yet. This application's own shell middleware records what
// happens to anything that reads a viewer there: signed-in people got the
// signed-out sidebar, silently, because the prop was built eagerly. The late
// hook is called from the first translation instead — after the handler, and
// so after authentication.
//
// Frozen afterwards, also deliberately. A translator that kept answering the
// current value would render the top of a page in one language and the bottom
// in another the moment anything resolved late, and that is harder to notice
// than a whole page in the wrong language.
func (c *Catalog) Deferred(initial Resolution, late func() (string, bool)) *Translator {
	if initial.Locale == "" {
		initial = c.neg.Default()
	}
	return &Translator{r: &request{cat: c, res: initial, late: late}}
}

// resolve settles the locale and returns it. The first call consults the late
// source; every call after it returns the same answer.
func (r *request) resolve() Resolution {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return r.res
	}
	r.frozen = true
	if r.late != nil {
		if locale, ok := r.late(); ok {
			if res, matched := r.cat.neg.Lookup(locale); matched {
				res.Source = SourceUser
				r.res = res
			}
		}
	}
	return r.res
}

// Prefer sets the request's locale, overriding every other source.
//
// It returns [ErrLocaleAlreadyUsed] once anything on this request has been
// translated, rather than silently doing nothing — a silent no-op here is the
// eager-prop bug a second time, and the caller would have no way to find out
// that the page it is about to render disagrees with the sentence it already
// rendered.
func (t *Translator) Prefer(locale string) error {
	t.r.mu.Lock()
	defer t.r.mu.Unlock()
	if t.r.frozen {
		return ErrLocaleAlreadyUsed
	}
	res, ok := t.r.cat.neg.Lookup(locale)
	if !ok {
		return &UnsupportedLocaleError{Locale: locale, Supported: t.r.cat.Locales()}
	}
	res.Source = SourceExplicit
	t.r.res = res
	t.r.frozen = true
	return nil
}

// UnsupportedLocaleError is returned when a locale is asked for that the
// application does not support.
type UnsupportedLocaleError struct {
	Locale    string
	Supported []string
}

func (e *UnsupportedLocaleError) Error() string {
	return "i18n: " + e.Locale + " is not a supported locale; this application supports " +
		display(e.Supported)
}

// Get returns the message for a key, with its placeholders substituted.
//
// A key the catalogue does not have is returned as itself, which is Laravel's
// behaviour and the right one: a reader seeing "profile.header" has been
// served a legible page with an obvious defect in it, where an error would
// have served no page at all. The miss is counted on the catalogue, which is
// how it gets fixed before a reader finds it.
func (t *Translator) Get(key string, repl ...R) string {
	res := t.r.resolve()
	return t.r.cat.render(res.Locale, key, 0, false, merge(repl))
}

// Choice returns the plural form of a message for a count.
//
// The count is bound to :count unless the caller supplied it, matching
// Laravel, so a message reads ":count posts" with nothing passed.
//
// An int rather than a float, and that is what makes the browser half
// provably equivalent — see [Form].
func (t *Translator) Choice(key string, n int, repl ...R) string {
	res := t.r.resolve()
	values := merge(repl)
	if _, ok := values["count"]; !ok {
		if values == nil {
			values = R{}
		}
		values["count"] = n
	}
	return t.r.cat.render(res.Locale, key, n, true, values)
}

// Has reports whether the catalogue defines a key for this request's locale
// or the default.
//
// It does not count a miss, because asking is not the same as needing: this
// is what lets a caller choose between a translated label and a fallback
// without the catalogue reporting a problem that is not one.
func (t *Translator) Has(key string) bool {
	res := t.r.resolve()
	if _, ok := t.r.cat.lookup(res.Locale, key); ok {
		return true
	}
	if _, ok := t.r.cat.lookup(t.r.cat.Default(), key); ok {
		return true
	}
	_, ok := t.r.cat.builtin[key]
	return ok
}

// Locale returns the request's locale, settling it if it is not settled.
func (t *Translator) Locale() string { return t.r.resolve().Locale }

// Tag returns the request's parsed language tag.
func (t *Translator) Tag() language.Tag { return t.r.resolve().Tag }

// Dir returns "ltr" or "rtl" for the request's locale.
func (t *Translator) Dir() string { return Dir(t.r.resolve().Tag) }

// Resolution returns the settled locale and where it came from.
func (t *Translator) Resolution() Resolution { return t.r.resolve() }

// Catalog returns the catalogue behind this translator.
func (t *Translator) Catalog() *Catalog { return t.r.cat }

// For returns a translator fixed to another locale, sharing this catalogue.
//
// This is what a mail channel uses once it knows a recipient's preference,
// and what satisfies the framework's Localizer seam.
func (t *Translator) For(locale string) *Translator { return t.r.cat.In(locale) }

// Export returns this request's locale as the browser half reads it.
func (t *Translator) Export() map[string]any {
	return t.r.cat.Export(t.r.resolve().Locale)
}

// merge flattens the variadic replacement sets, later ones winning.
//
// Variadic so that the common call has no second argument at all, and merging
// rather than taking the first so that a caller can add to a set it was
// handed without copying it.
func merge(sets []R) R {
	switch len(sets) {
	case 0:
		return nil
	case 1:
		return sets[0]
	}
	out := R{}
	for _, set := range sets {
		for key, value := range set {
			out[key] = value
		}
	}
	return out
}
