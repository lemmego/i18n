// Package provider wires lemmego/i18n into an application.
//
// It is a subpackage rather than the module root because it imports api/app,
// which reaches api/db and therefore a SQL driver. A worker rendering a mail
// subject imports the root and pays for none of that — see i18n/deps_test.go,
// which fails if the split stops being real.
package provider

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lemmego/api/app"
	"github.com/lemmego/i18n"
)

var (
	_ app.Provider            = (*Provider)(nil)
	_ app.PublishableProvider = (*Provider)(nil)
)

// DefaultPath is where an application's catalogue lives.
//
// Under resources/ rather than at the project root, where Laravel puts it,
// and that is not a style choice. A Dockerfile copies resources/ wholesale
// already — the one in the application this was built against carries a
// comment saying so, added after resources/mail was left out once and the
// image served no email templates. A new top-level directory read at runtime
// would be the same mistake a second time: the image boots, serves the
// default language, and can serve nothing else.
const DefaultPath = "resources/lang"

// DefaultCookie is the cookie a reader's chosen locale is remembered in.
const DefaultCookie = "locale"

// Provider registers a translator and resolves each request's locale.
type Provider struct {
	// Supported lists the locales, the first being the default and the
	// fallback. Empty reads I18N_LOCALES, then falls back to English only.
	Supported []string

	// Path is the catalogue directory. Empty reads I18N_PATH, then
	// DefaultPath.
	Path string

	// FS is a catalogue to read instead of the filesystem, for a deployment
	// that ships one binary and no resources directory:
	//
	//	//go:embed resources/lang
	//	var langFS embed.FS
	//
	//	&provider.Provider{FS: langFS, Root: "resources/lang"}
	//
	// Set it for production. The filesystem is the convenient default for
	// development, where a translator edits a file and reloads.
	FS   fs.FS
	Root string

	// Catalog, if set, is used as-is and nothing is loaded. For a test, or an
	// application that assembles its own.
	Catalog *i18n.Catalog

	// Mount is the catalogues modules ship, keyed by namespace:
	//
	//	Mount: map[string]fs.FS{"authz": authz.Catalogue()}
	//
	// which makes authz::errors.forbidden resolvable, and lets an
	// application override one message by publishing
	// resources/lang/vendor/authz/<locale>/errors.json and editing it.
	//
	// Mounted after the application's own files are read and before
	// anything can translate, which is the only window there is: the
	// vendor/ overrides have to already be loaded for a module's message
	// to lose to an application's, and a request must not be able to
	// observe a half-mounted catalogue.
	Mount map[string]fs.FS

	// CookieName is where a chosen locale is remembered. Empty means
	// DefaultCookie; "-" disables the cookie entirely.
	CookieName string

	// QueryKey enables ?lang=fr when set. Off by default: it is a switch
	// anybody can flip on anybody else's link, which is fine for development
	// and for a language picker and wrong as an always-on default.
	QueryKey string

	// UserLocale returns a signed-in reader's stored preference.
	//
	// Called lazily, on the request's first translation — not from the
	// middleware. The middleware wraps the mux, so it runs before the route's
	// own authentication handler and there is no reader to ask yet. This is
	// the same trap an eager shared prop fell into once, where signed-in
	// people got the signed-out sidebar; here it would be the wrong language.
	UserLocale func(c app.Context) (string, bool)

	catalog *i18n.Catalog
	app     app.App
}

func (p *Provider) Provide(a app.App) error {
	catalog, err := p.Load()
	if err != nil {
		return err
	}
	p.catalog = catalog
	// Kept so the late hook can build a context the application's own code
	// can use: UserLocale almost certainly reads a repository out of the
	// container, and a context with no application would hand it nil.
	p.app = a

	// Registered under the interface, not the concrete type: api/app's own
	// documentation records that Service keys on the dynamic type, so an
	// interface-typed service has to go in and come out through
	// Lookup/Get.
	app.RegisterIfAbsent[app.Localizer](a, localizer{catalog.In(catalog.Default())})

	// The process-wide default, so a worker, a CLI command or a code path
	// nobody thought about gets sentences rather than keys.
	i18n.SetDefault(catalog.In(catalog.Default()))

	// Installed here rather than returned from AddMiddlewares, because
	// AddMiddlewares goes to the router's per-route chain, which does not run
	// for Handle/HandleFunc routes or for the 404 catch-all. A 404 page in
	// the wrong language is exactly the kind of thing nobody notices for a
	// year.
	a.Router().Use(p.Negotiate())
	return nil
}

// Load builds the catalogue this provider would register.
//
// Exported so an application can assemble the catalogue without booting — to
// mount a module's messages before Provide runs, or to assert in a test that
// the files on disk parse and resolve. Provide calls it exactly once.
func (p *Provider) Load() (*i18n.Catalog, error) {
	if p.Catalog != nil {
		return p.mount(p.Catalog, nil)
	}

	supported := p.Supported
	if len(supported) == 0 {
		supported = splitLocales(env("I18N_LOCALES"))
	}
	if len(supported) == 0 {
		supported = []string{"en"}
	}

	if p.FS != nil {
		root := p.Root
		if root == "" {
			root = "."
		}
		return p.mount(i18n.LoadFS(p.FS, root, supported...))
	}

	path := p.Path
	if path == "" {
		path = env("I18N_PATH")
	}
	if path == "" {
		path = DefaultPath
	}
	return p.mount(i18n.LoadDir(locate(path), supported...))
}

// Catalogue returns the loaded catalogue, for an application that wants to
// mount a module's messages or inspect the misses.
//
// Before Provide has run it answers with whatever Catalog was set, which is
// what makes Negotiate drivable from a test without booting an application.
func (p *Provider) Catalogue() *i18n.Catalog {
	if p.catalog != nil {
		return p.catalog
	}
	return p.Catalog
}

// Negotiate returns the middleware that settles each request's locale.
func (p *Provider) Negotiate() app.HTTPMiddleware {
	cookieName := p.CookieName
	if cookieName == "" {
		cookieName = DefaultCookie
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			catalog := p.Catalogue()
			neg := catalog.Negotiator()
			resolved, source := p.resolve(r, neg, cookieName)

			// Remembered now, while the locale is known and before the
			// handler writes anything.
			//
			// Only for the query parameter, and that is the one case that
			// needs it: validation errors are rendered in the locale of the
			// failing POST and read back after a redirect, so without
			// persisting it a reader who arrived on ?lang=fr would see French
			// everywhere except their own error messages. A cookie or a
			// header needs no help. Doing it here also means no
			// ResponseWriter wrapper — and a wrapper that forgot to forward
			// http.Flusher would turn every event stream into a response that
			// delivers nothing, which this framework has shipped once already.
			if source == i18n.SourceQuery && cookieName != "-" {
				http.SetCookie(w, &http.Cookie{
					Name:     cookieName,
					Value:    resolved.Locale,
					Path:     "/",
					HttpOnly: true, // nothing on the client reads it; the locale arrives as data
					Secure:   r.TLS != nil,
					SameSite: http.SameSiteLaxMode,
					Expires:  time.Now().AddDate(1, 0, 0),
				})
			}

			resolved.Source = source
			translator := catalog.Deferred(resolved, p.lateLocale(r))

			// On both keys, and that is not belt-and-braces.
			//
			// api's key is what ctx.T() reads, so the validator and the HTTP
			// sentinels find it. i18n's own key is what i18n.LocaleOf reads,
			// and that is the one mail accepts — func(context.Context)
			// string, so mail never imports this module. Without it, a
			// verification email sent from inside a French request would
			// render in English, because the only thing that knew the locale
			// was a key mail cannot see.
			ctx := app.ContextWithTranslator(r.Context(), translator)
			ctx = i18n.With(ctx, translator)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolve runs the cheap sources, in order of precedence.
func (p *Provider) resolve(r *http.Request, neg *i18n.Negotiator, cookieName string) (i18n.Resolution, i18n.Source) {
	if p.QueryKey != "" {
		if value := r.URL.Query().Get(p.QueryKey); value != "" {
			if res, ok := neg.Lookup(value); ok {
				return res, i18n.SourceQuery
			}
		}
	}
	if cookieName != "-" {
		if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
			if res, ok := neg.Lookup(cookie.Value); ok {
				return res, i18n.SourceCookie
			}
		}
	}
	if res, ok := neg.FromHeader(r.Header.Get("Accept-Language")); ok {
		return res, i18n.SourceHeader
	}
	return neg.Default(), i18n.SourceDefault
}

// lateLocale adapts UserLocale into the hook the translator calls on first
// use.
//
// It builds an app.Context at that point, which is after the handler has run
// its own authentication, which is the whole reason this is not resolved in
// the middleware.
func (p *Provider) lateLocale(r *http.Request) func() (string, bool) {
	if p.UserLocale == nil {
		return nil
	}
	return func() (string, bool) {
		defer func() {
			// A stored preference usually means a database read, and a panic
			// there must cost one reader their language rather than the whole
			// request. The framework's own event emitter was criticised for
			// lacking exactly this.
			if problem := recover(); problem != nil {
				slog.Error("i18n: reading the reader's locale panicked", "panic", problem)
			}
		}()
		return p.UserLocale(app.NewContext(p.app, nil, r))
	}
}

// From returns the request's translator.
//
// Never nil: a request that somehow reached a handler without the middleware
// gets the process default, so a page renders in the default language rather
// than panicking.
func From(c app.Context) *i18n.Translator {
	if t, ok := app.TranslatorFromContext(c.RequestContext()).(*i18n.Translator); ok && t != nil {
		return t
	}
	return i18n.FromContext(c.RequestContext())
}

// Prefer overrides the request's locale from a handler.
//
// It returns i18n.ErrLocaleAlreadyUsed once anything on this request has been
// translated, rather than silently doing nothing — the caller would otherwise
// have no way to know the page it is about to render disagrees with the
// sentence it already rendered.
func Prefer(c app.Context, locale string) error {
	return From(c).Prefer(locale)
}

// Remember writes a reader's chosen locale to the cookie, for a language
// picker.
func (p *Provider) Remember(c app.Context, locale string) error {
	if err := Prefer(c, locale); err != nil {
		return err
	}
	cookieName := p.CookieName
	if cookieName == "" {
		cookieName = DefaultCookie
	}
	if cookieName == "-" {
		return nil
	}
	r := c.Request()
	http.SetCookie(c.ResponseWriter(), &http.Cookie{
		Name:     cookieName,
		Value:    locale,
		Path:     "/",
		HttpOnly: true,
		Secure:   r != nil && r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().AddDate(1, 0, 0),
	})
	return nil
}

// localizer adapts *i18n.Translator to app.Localizer.
//
// The adapter exists for one reason: For returns a concrete
// *i18n.Translator and the interface wants an app.Translator, and Go demands
// the exact return type. Everything else is promoted from the embedded value.
type localizer struct{ *i18n.Translator }

func (l localizer) For(locale string) app.Translator { return l.Translator.For(locale) }

// AddPublishables offers the framework's own English for editing.
//
// This is Laravel's lang:publish with no new command: api/app/publish.go
// already has the --tags selector and already refuses to overwrite a file
// somebody has edited, so `lemmego run publish --tags=i18n` is the whole
// feature.
func (p *Provider) AddPublishables() []*app.Publishable {
	path := p.Path
	if path == "" {
		path = env("I18N_PATH")
	}
	if path == "" {
		path = DefaultPath
	}

	var out []*app.Publishable
	builtin := i18n.Builtin()
	entries, err := fs.ReadDir(builtin, "en")
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(builtin, "en/"+entry.Name())
		if err != nil {
			continue
		}
		out = append(out, &app.Publishable{
			FilePath: filepath.Join(path, "en", entry.Name()),
			Content:  data,
			Tag:      TagCatalog,
		})
	}
	return out
}

// TagCatalog selects this provider's publishables.
//
// Namespaced, because --tags is a hard selector that errors on an unknown
// tag, so two providers sharing a generic name like "config" would be
// indistinguishable.
const TagCatalog = "i18n"

// env reads a variable, treating present-and-empty as absent.
//
// Deliberately not config.MustEnv. That helper branches on whether the
// variable exists, so a stray "I18N_PATH=" line in a production .env is a
// decision rather than a default — and it panics on a value it cannot parse,
// which would take the process down before anything could log why. Both have
// been closed in this repository twice already, in api/logger and in mail,
// which is a sign about the helper's shape rather than about its callers.
func env(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func splitLocales(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// locate resolves a relative catalogue path by searching upward.
//
// This is api/config's findEnvFile, for the same reason and against the same
// failure. `go test ./internal/...` runs each package with the working
// directory set to that package, so a relative "resources/lang" does not
// exist from there — and because a missing catalogue directory is
// deliberately not an error, the result is not a failure anybody can see. The
// application boots, every key renders as itself, and the suite reports that
// as a page whose text has simply changed.
//
// An absolute path is returned untouched: a caller who wrote one meant it.
// So is a relative path that resolves from here, which is the normal case of
// a binary started at the project root. Only when neither holds does this
// walk up, and it stops at the first ancestor that has the directory, or at a
// go.mod — past the module there is nothing of this project to find, and
// walking to / would let one project read another's catalogue.
func locate(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}

	dir, err := os.Getwd()
	if err != nil {
		return path
	}
	for {
		candidate := filepath.Join(dir, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir = parent
	}
}

// mount folds the module catalogues in, so Load has one return path.
func (p *Provider) mount(cat *i18n.Catalog, err error) (*i18n.Catalog, error) {
	if err != nil {
		return nil, err
	}
	for namespace, fsys := range p.Mount {
		if err := cat.Mount(namespace, fsys, "."); err != nil {
			return nil, fmt.Errorf("i18n: mounting %q: %w", namespace, err)
		}
	}
	return cat, nil
}
