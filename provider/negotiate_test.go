package provider_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemmego/api/app"
	"github.com/lemmego/i18n"
	"github.com/lemmego/i18n/provider"
)

func testCatalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	cat, err := i18n.NewCatalog("en", "fr", "de")
	if err != nil {
		t.Fatal(err)
	}
	for locale, hello := range map[string]string{
		"en": "Hello",
		"fr": "Bonjour",
		"de": "Hallo",
	} {
		if err := cat.AddMessages(locale, map[string]any{"page.hello": hello}); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

// drive runs one request through the middleware and returns what the handler
// translated, plus the response so a cookie can be inspected.
func drive(t *testing.T, p *provider.Provider, build func(*http.Request)) (string, *http.Response) {
	t.Helper()

	var translated string
	handler := p.Negotiate()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translated = app.TranslatorFromContext(r.Context()).Get("page.hello")
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if build != nil {
		build(r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return translated, w.Result()
}

func TestAcceptLanguageIsHonoured(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t)}
	got, _ := drive(t, p, func(r *http.Request) {
		r.Header.Set("Accept-Language", "fr-CH, fr;q=0.9, en;q=0.1")
	})
	if got != "Bonjour" {
		t.Errorf("got %q, want the French", got)
	}
}

func TestTheCookieBeatsTheHeader(t *testing.T) {
	// A reader who chose a language has said more than their browser's
	// default did.
	p := &provider.Provider{Catalog: testCatalog(t)}
	got, _ := drive(t, p, func(r *http.Request) {
		r.Header.Set("Accept-Language", "de")
		r.AddCookie(&http.Cookie{Name: "locale", Value: "fr"})
	})
	if got != "Bonjour" {
		t.Errorf("got %q, want the cookie's French", got)
	}
}

func TestTheQueryParameterIsOffByDefault(t *testing.T) {
	// It is a switch anybody can flip on anybody else's link, which is fine
	// for a language picker and wrong as an always-on default.
	p := &provider.Provider{Catalog: testCatalog(t)}
	got, response := drive(t, p, func(r *http.Request) {
		r.URL.RawQuery = "lang=fr"
	})
	if got != "Hello" {
		t.Errorf("got %q; ?lang= must do nothing unless QueryKey is set", got)
	}
	if len(response.Cookies()) != 0 {
		t.Error("a cookie was set for a query parameter that was not honoured")
	}
}

func TestTheQueryParameterWinsAndIsRemembered(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t), QueryKey: "lang"}
	got, response := drive(t, p, func(r *http.Request) {
		r.Header.Set("Accept-Language", "de")
		r.URL.RawQuery = "lang=fr"
	})
	if got != "Bonjour" {
		t.Errorf("got %q, want the query's French", got)
	}

	// Remembered because validation errors are rendered in the locale of the
	// failing POST and read back after a redirect. Without the cookie, a
	// reader who arrived on ?lang=fr would get French everywhere except their
	// own error messages.
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "locale" || cookies[0].Value != "fr" {
		t.Fatalf("cookies = %v, want locale=fr", cookies)
	}
	if !cookies[0].HttpOnly {
		t.Error("the locale cookie is readable by scripts; nothing on the client " +
			"needs it, because the locale arrives as data")
	}
	if cookies[0].Path != "/" {
		t.Errorf("cookie path = %q, want /", cookies[0].Path)
	}
}

func TestAnUnsupportedValueFallsThroughRatherThanSticking(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t), QueryKey: "lang"}
	got, _ := drive(t, p, func(r *http.Request) {
		r.URL.RawQuery = "lang=ja"
		r.Header.Set("Accept-Language", "de")
	})
	if got != "Hallo" {
		t.Errorf("got %q; an unsupported query value must fall through to the next "+
			"source, not stop the chain", got)
	}
}

func TestTheDefaultAnswersWhenNothingMatches(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t)}
	got, _ := drive(t, p, func(r *http.Request) {
		r.Header.Set("Accept-Language", "ja, ko;q=0.5")
	})
	if got != "Hello" {
		t.Errorf("got %q, want the default", got)
	}
}

// TestTheStoredPreferenceIsReadAfterTheHandlerStarts is the rule the lazy
// design exists for.
//
// The middleware wraps the mux, so it runs before the route's own
// authentication handler and there is no reader to ask yet. An eager
// implementation reads nothing and serves the browser's language.
func TestTheStoredPreferenceIsReadAfterTheHandlerStarts(t *testing.T) {
	var (
		authenticated bool
		asked         int
	)
	p := &provider.Provider{
		Catalog: testCatalog(t),
		UserLocale: func(app.Context) (string, bool) {
			asked++
			if !authenticated {
				// What an eager read would see: nobody is signed in yet.
				return "", false
			}
			return "fr", true
		},
	}

	var translated string
	handler := p.Negotiate()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Standing in for the route's own auth handler, which runs here and
		// not in the middleware.
		authenticated = true
		translated = app.TranslatorFromContext(r.Context()).Get("page.hello")
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "de")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if translated != "Bonjour" {
		t.Errorf("got %q, want the reader's stored French — the preference was read "+
			"before the handler had a chance to authenticate anybody", translated)
	}
	if asked != 1 {
		t.Errorf("the stored preference was read %d times; it is likely a database "+
			"query, so it is read once per request", asked)
	}
}

func TestASignedOutRequestDoesNotInheritTheLastReadersLocale(t *testing.T) {
	// The translator is per-request, so a process-level cache would show up
	// here as the second request answering in the first reader's language.
	p := &provider.Provider{
		Catalog: testCatalog(t),
		UserLocale: func(c app.Context) (string, bool) {
			if c.Request().Header.Get("X-Reader") == "ada" {
				return "fr", true
			}
			return "", false
		},
	}

	first, _ := drive(t, p, func(r *http.Request) { r.Header.Set("X-Reader", "ada") })
	if first != "Bonjour" {
		t.Fatalf("the signed-in reader got %q", first)
	}
	second, _ := drive(t, p, nil)
	if second != "Hello" {
		t.Errorf("the next request got %q, which is the previous reader's language", second)
	}
}

// TestAPanicReadingThePreferenceCostsOneLanguageNotTheRequest: the hook is
// usually a database read, and the framework's own event emitter was
// criticised for having no recover around its listeners.
func TestAPanicReadingThePreferenceCostsOneLanguageNotTheRequest(t *testing.T) {
	p := &provider.Provider{
		Catalog:    testCatalog(t),
		UserLocale: func(app.Context) (string, bool) { panic("the database is down") },
	}

	got, response := drive(t, p, nil)
	if got != "Hello" {
		t.Errorf("got %q, want the default after the hook panicked", got)
	}
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d; a failed locale lookup must not fail the page",
			response.StatusCode)
	}
}

// TestNoSessionIsTouched is why the locale is kept in a cookie.
//
// PutSession returns nil and logs "session not set" when no session provider
// is registered, and auth supports DisableSession — so a session-backed
// locale store is one that silently does nothing in a legitimate deployment.
// This request has no session of any kind.
func TestNoSessionIsTouched(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t)}
	got, _ := drive(t, p, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "locale", Value: "de"})
	})
	if got != "Hallo" {
		t.Errorf("got %q, want the German from the cookie with no session anywhere", got)
	}
}

func TestPublishablesAreNamespacedAndCarryTheEnglish(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t)}
	published := p.AddPublishables()
	if len(published) == 0 {
		t.Fatal("nothing is published, so there is no way to edit the framework's " +
			"own English")
	}

	var sawValidation bool
	for _, item := range published {
		if item.Tag != provider.TagCatalog {
			t.Errorf("%s carries the tag %q; --tags is a hard selector, so a generic "+
				"tag would collide with another provider's", item.FilePath, item.Tag)
		}
		if !strings.HasPrefix(item.FilePath, provider.DefaultPath+"/en/") {
			t.Errorf("%s is published outside the catalogue directory", item.FilePath)
		}
		if strings.HasSuffix(item.FilePath, "validation.json") {
			sawValidation = true
			if !strings.Contains(string(item.Content), "This field is required") {
				t.Error("the published validation.json does not hold the English it " +
					"is meant to let somebody edit")
			}
		}
	}
	if !sawValidation {
		t.Error("validation.json is not published, which is the one file an " +
			"application is most likely to want to edit")
	}
}

// TestTheLocaleReachesMailsOwnSeam is why the middleware writes two context
// keys instead of one.
//
// api's key is what ctx.T() reads. i18n.LocaleOf reads i18n's own, and that
// is the seam mail accepts — func(context.Context) string, so mail never
// imports i18n. With only api's key set, a verification email sent from
// inside a French request renders in English, because the one thing that knew
// the locale was behind a key mail cannot see.
func TestTheLocaleReachesMailsOwnSeam(t *testing.T) {
	p := &provider.Provider{Catalog: testCatalog(t)}

	var locale string
	handler := p.Negotiate()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale = i18n.LocaleOf(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "locale", Value: "fr"})
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if locale != "fr" {
		t.Errorf("i18n.LocaleOf = %q, want \"fr\" — mail reads this seam, so a "+
			"message sent from inside this request would go out in the wrong "+
			"language", locale)
	}
}

// TestTheCatalogueIsFoundFromASubdirectory is api/config's findEnvFile
// lesson, applied to the one other thing this framework reads from a relative
// path at boot.
//
// `go test ./internal/...` runs each package with the working directory set to
// that package, so "resources/lang" does not exist from there. A missing
// catalogue directory is deliberately not an error — an application without
// one is valid and falls back to the framework's English — so the symptom is
// not a failure but a page where every key renders as itself.
func TestTheCatalogueIsFoundFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	lang := filepath.Join(root, "resources", "lang", "en")
	if err := os.MkdirAll(lang, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lang, "page.json"),
		[]byte(`{"hello":"Hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The module boundary the search stops at, so one project cannot read
	// another's catalogue by walking to the filesystem root.
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deep := filepath.Join(root, "internal", "routes")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)

	p := &provider.Provider{Supported: []string{"en"}}
	cat, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.In("en").Get("page.hello"); got != "Hello" {
		t.Errorf("page.hello = %q — the catalogue was not found from a "+
			"subdirectory, so every key on every page renders as itself and "+
			"nothing reports it", got)
	}
}

// TestTheSearchStopsAtTheModule: without the go.mod boundary the walk reaches
// the filesystem root, and a project nested under another would silently read
// its parent's catalogue.
func TestTheSearchStopsAtTheModule(t *testing.T) {
	outer := t.TempDir()
	lang := filepath.Join(outer, "resources", "lang", "en")
	if err := os.MkdirAll(lang, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lang, "page.json"),
		[]byte(`{"hello":"the outer project"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "go.mod"),
		[]byte("module inner.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inner)

	p := &provider.Provider{Supported: []string{"en"}}
	cat, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.In("en").Get("page.hello"); got != "page.hello" {
		t.Errorf("page.hello = %q; the inner module read the outer project's "+
			"catalogue", got)
	}
}
