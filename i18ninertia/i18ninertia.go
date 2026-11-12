// Package i18ninertia hands a request's catalogue to the browser.
//
// It is a subpackage rather than the module root, and prefix-named rather than
// i18n/inertia, for two separate reasons. The prefix is so the import does not
// collide with lemmego/inertia at every call site, the way observe/watch's
// observeslog and observehttp are named. The split is so the root keeps its
// three non-standard packages: this one reaches lemmego/inertia and
// romsar/gonertia, and a worker rendering a mail subject should pay for
// neither. deps_test.go fails if that stops being true.
//
// It also cannot live in api. lemmego/inertia requires lemmego/api, so that
// edge only goes one way.
package i18ninertia

import (
	"context"
	"errors"
	"net/http"

	"github.com/lemmego/api/app"
	"github.com/lemmego/i18n"
	"github.com/lemmego/inertia"
	gonertia "github.com/romsar/gonertia/v3"
)

var _ app.Provider = (*Provider)(nil)

// PropKey is the page prop the catalogue arrives under.
const PropKey = "i18n"

// TemplateKey is the root template datum carrying the locale into <html>.
const TemplateKey = "htmlLang"

// Payload is what the browser reads.
//
// It mirrors js/i18n.d.mts exactly, and the two are held together by a test
// rather than by care. Flat dotted keys with plural sets as leaf objects,
// because the Go key space is dotted, so there is one key format in the
// system and t() is a map lookup rather than a path walk.
type Payload struct {
	Locale   string         `json:"locale"`
	Fallback string         `json:"fallback"`
	Dir      string         `json:"dir"`
	Messages map[string]any `json:"messages"`
}

// PayloadFor renders one translator's catalogue.
//
// Fallback travels as well as the active locale, because the browser cannot
// fall back to a catalogue it was never sent — Catalog.Export already folds
// the framework's English and the default locale in underneath for the same
// reason.
func PayloadFor(t *i18n.Translator) Payload {
	return Payload{
		Locale:   t.Locale(),
		Fallback: t.Catalog().Default(),
		Dir:      t.Dir(),
		Messages: t.Export(),
	}
}

// ErrNoInertia is returned when this provider runs before Inertia's.
var ErrNoInertia = errors.New(
	"i18ninertia: no Inertia instance is registered, so there is nothing to " +
		"share a catalogue with; list &inertia.Provider{} before this one in " +
		"LoadProviders")

// Provider shares the catalogue and the <html lang> datum.
//
// List it after both &inertia.Provider{} and i18n's own provider: it needs
// Inertia's instance from the container at boot, and the datum middleware has
// to run after the one that resolves the locale.
type Provider struct{}

func (p *Provider) Provide(a app.App) error {
	if err := Share(a); err != nil {
		return err
	}
	a.Router().Use(HTMLLang())
	return nil
}

// Share registers the catalogue as a page prop.
//
// Three things about this one call are load-bearing, and the obvious spelling
// gets each of them wrong.
//
// It is a *shared* prop holding gonertia's own Once rather than one set on
// the request context with lemmego's, and the difference is invisible until
// you measure bandwidth. inertia.SetProp hands its value to gonertia
// unconverted — the translation into gonertia's wrapper happens only inside
// Respond — and gonertia resolves the *value* correctly either way, because
// lemmego's OnceProp has a Prop() method and so satisfies gonertia.Proper.
// What it does not satisfy is gonertia's partial-reload filter, which type
// asserts its own OnceProp by name. So a middleware-registered
// inertia.Once(...) renders every message correctly and silently loses the
// once semantics, re-sending the whole catalogue on every partial reload.
// There is no wrong string to notice, which is why it has to be a test.
//
// It holds a func(context.Context) any rather than a value, so one
// registration at boot resolves per request — gonertia unwraps the Once and
// then calls the closure with the request's context.
//
// And it is Once rather than Always or Optional. Once is sent on a document
// request and dropped from partial reloads, which is what makes a 60KB
// catalogue cost one payload per page session instead of one per visit.
// Optional would send nothing on the first load; Always would re-send
// everything on every partial.
//
// The closure runs in its own goroutine, one per prop, and under strict SSR
// the whole render can happen twice on a warmup retry — so it reads and
// allocates and mutates nothing shared.
func Share(a app.App) error {
	// Lookup rather than inertia.Get, which wraps app.Get and panics when the
	// service is absent. A provider ordering mistake should name itself, not
	// take down the boot with a container panic.
	in, ok := app.Lookup[*inertia.Inertia](a)
	if !ok || in == nil {
		return ErrNoInertia
	}
	in.ShareProp(PropKey, PropValue())
	return nil
}

// PropValue is the value Share registers.
//
// Split out and given a concrete return type on purpose: gonertia's
// partial-reload filter recognises a once-prop by asserting its own
// OnceProp type by name, so the wrapper being *that* type and not
// lemmego's look-alike is the whole behaviour. A signature is the one
// place a compiler can hold that for us.
func PropValue() gonertia.OnceProp {
	return gonertia.Once(func(ctx context.Context) any {
		return PayloadFor(TranslatorFrom(ctx))
	})
}

// TranslatorFrom reads the request's translator, from either key it may be on.
//
// i18n's provider writes both: api's, which ctx.T() reads, and i18n's own,
// which i18n.LocaleOf reads. Trying api's first means this works even against
// a provider that only sets that one. It never returns nil — a page in the
// default language beats a page that panicked.
func TranslatorFrom(ctx context.Context) *i18n.Translator {
	if t, ok := app.TranslatorFromContext(ctx).(*i18n.Translator); ok && t != nil {
		return t
	}
	return i18n.FromContext(ctx)
}

// Lang carries the locale into the root template:
//
//	<html lang="{{ .htmlLang.Lang }}" dir="{{ .htmlLang.Dir }}">
//
// A pointer with methods rather than a string or a closure, and that is the
// whole reason this type exists. Template data is copied into a fresh map at
// render time and html/template calls no func() any, so a value would be
// frozen before the locale settles and a closure would never be called. A
// method is invoked at execute time, which gonertia reaches only after props
// have resolved — and resolving the prop above is what freezes the locale, so
// the attribute and the body cannot disagree.
//
// It is rendered in Go, so it survives an SSR failure, where Inertia's own
// head comes back empty. That is seo's argument for the same mechanism.
type Lang struct{ t *i18n.Translator }

// Lang returns the BCP 47 tag for the html lang attribute.
func (l *Lang) Lang() string {
	if l == nil || l.t == nil {
		return ""
	}
	return l.t.Locale()
}

// Dir returns "ltr" or "rtl", computed from the tag's script in Go rather
// than guessed from a list of locale names in JavaScript.
func (l *Lang) Dir() string {
	if l == nil || l.t == nil {
		return "ltr"
	}
	return l.t.Dir()
}

// LangFrom builds the datum for a request.
//
// It holds the translator rather than its locale, which is the whole
// point: the locale is read when the template calls the method.
func LangFrom(ctx context.Context) *Lang {
	return &Lang{t: TranslatorFrom(ctx)}
}

// HTMLLang installs the template datum.
//
// It re-sets the request rather than trusting the context it was handed.
// gonertia's SetTemplateDatum mutates the template-data map in place when one
// already exists, so in an application whose seo middleware ran first the
// write would be visible anyway — and in one without seo it would be visible
// only through the returned context. Depending on another module's middleware
// order for correctness is how this would work in one project and silently
// not in the next.
func HTMLLang() app.HTTPMiddleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := inertia.SetTemplateDatum(r.Context(), TemplateKey,
				LangFrom(r.Context()))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
