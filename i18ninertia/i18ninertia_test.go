package i18ninertia_test

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/lemmego/i18n"
	"github.com/lemmego/i18n/i18ninertia"
	"github.com/lemmego/inertia"
	gonertia "github.com/romsar/gonertia/v3"
)

func catalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	cat, err := i18n.NewCatalog("en", "fr", "ar")
	if err != nil {
		t.Fatal(err)
	}
	for locale, messages := range map[string]map[string]any{
		"en": {"page.hello": "Hello", "page.posts": map[string]string{"one": ":count post", "other": ":count posts"}},
		"fr": {"page.hello": "Bonjour"},
		"ar": {"page.hello": "مرحبا"},
	} {
		if err := cat.AddMessages(locale, messages); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

// TestThePropIsResolvedPerRequestAndNotAtBoot is the shape the whole design
// rests on: one registration, one closure, called with each request's context.
func TestThePropIsResolvedPerRequestAndNotAtBoot(t *testing.T) {
	cat := catalog(t)

	prop := i18ninertia.PropValue()
	inner, ok := prop.Prop().(func(context.Context) any)
	if !ok {
		t.Fatalf("the prop holds %T; gonertia only calls func() any and "+
			"func(context.Context) any, so anything else is sent to the browser "+
			"verbatim", prop.Prop())
	}

	for _, locale := range []string{"en", "fr"} {
		ctx := i18n.With(context.Background(), cat.In(locale))
		payload, ok := inner(ctx).(i18ninertia.Payload)
		if !ok {
			t.Fatalf("the closure returned %T, not a Payload", inner(ctx))
		}
		if payload.Locale != locale {
			t.Errorf("payload.Locale = %q, want %q", payload.Locale, locale)
		}
	}
}

// TestTheWrapperIsGonertiasOwnOnce guards the one mistake here that has no
// visible symptom.
//
// A middleware-registered inertia.Once would render every message correctly,
// because lemmego's OnceProp has a Prop method and so satisfies
// gonertia.Proper. But gonertia's partial-reload filter asserts its *own*
// OnceProp by name, so lemmego's look-alike is not recognised and the whole
// catalogue is re-sent on every partial reload. Nothing renders wrongly;
// there is only more traffic. That is why this is asserted rather than
// commented.
func TestTheWrapperIsGonertiasOwnOnce(t *testing.T) {
	var prop any = i18ninertia.PropValue()
	if _, ok := prop.(gonertia.OnceProp); !ok {
		t.Errorf("the shared prop is %T, not gonertia.OnceProp. Partial reloads "+
			"will carry the entire catalogue, silently.", prop)
	}
	// And both halves of the trap, pinned as facts rather than left in a
	// comment. If upstream ever changes either one, this says so instead of
	// the guard quietly becoming obsolete or wrong.
	var lookalike any = inertia.Once(func() any { return nil })
	if _, ok := lookalike.(gonertia.Proper); !ok {
		t.Error("lemmego's OnceProp no longer satisfies gonertia.Proper, so a " +
			"middleware-registered prop would now break visibly rather than " +
			"silently, and this guard describes the wrong trap")
	}
	if _, ok := lookalike.(gonertia.OnceProp); ok {
		t.Error("lemmego's OnceProp is gonertia's own now, so inertia.SetProp " +
			"would be safe and this guard is obsolete")
	}

	// And the property that makes Once the right wrapper at all: it is sent on
	// a first load, unlike Optional and Defer, which gonertia skips there.
	if _, skips := prop.(interface{ ignoreFirstLoad() }); skips {
		t.Error("the wrapper is skipped on a first load, so the first page a " +
			"reader sees would have no messages")
	}
}

// TestThePayloadMatchesWhatTheBrowserDeclares reads the TypeScript rather
// than trusting that the two were written on the same day.
func TestThePayloadMatchesWhatTheBrowserDeclares(t *testing.T) {
	encoded, err := json.Marshal(i18ninertia.PayloadFor(catalog(t).In("en")))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}

	source, err := os.ReadFile("../js/i18n.d.mts")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)interface Payload \{(.*?)\}`).FindSubmatch(source)
	if block == nil {
		t.Fatal("js/i18n.d.mts has no Payload interface, so this test is not " +
			"reading what it thinks it is")
	}
	var declared []string
	for _, field := range regexp.MustCompile(`(?m)^\s*(\w+)\s*:`).FindAllSubmatch(block[1], -1) {
		declared = append(declared, string(field[1]))
	}
	if len(declared) < 4 {
		t.Fatalf("only found %v in the TypeScript Payload; the parse is wrong", declared)
	}

	for _, name := range declared {
		if _, ok := decoded[name]; !ok {
			t.Errorf("the browser declares %q and the Go payload does not send it", name)
		}
	}
	for name := range decoded {
		if !slices.Contains(declared, name) {
			t.Errorf("the Go payload sends %q and the browser does not declare it", name)
		}
	}
}

// TestTheFallbackAndTheFrameworksOwnEnglishBothTravel: the browser cannot
// fall back to a catalogue it was never sent.
func TestTheFallbackAndTheFrameworksOwnEnglishBothTravel(t *testing.T) {
	payload := i18ninertia.PayloadFor(catalog(t).In("fr"))

	if payload.Fallback != "en" {
		t.Errorf("Fallback = %q, want the catalogue's default", payload.Fallback)
	}
	if got := payload.Messages["page.hello"]; got != "Bonjour" {
		t.Errorf("page.hello = %v, want the French", got)
	}
	// Untranslated in French, so it has to arrive from the default locale.
	if _, ok := payload.Messages["page.posts"]; !ok {
		t.Error("a key the French catalogue does not have is missing from the " +
			"payload, so t() in the browser would render it as itself")
	}
	// And the framework's own floor, which no application file supplies.
	if _, ok := payload.Messages["validation.required"]; !ok {
		t.Error("the framework's embedded English is not in the payload, so a " +
			"client-side form could not render the message the server would")
	}
}

// TestPluralSetsArriveAsObjects, because Intl.PluralRules needs the forms,
// not a rendered sentence.
func TestPluralSetsArriveAsObjects(t *testing.T) {
	payload := i18ninertia.PayloadFor(catalog(t).In("en"))
	forms, ok := payload.Messages["page.posts"].(map[string]string)
	if !ok {
		t.Fatalf("page.posts is %T, want the form set", payload.Messages["page.posts"])
	}
	if forms["other"] != ":count posts" {
		t.Errorf("forms[other] = %q", forms["other"])
	}
}

// TestLangReadsTheLocaleWhenCalled is why Lang is a pointer with methods and
// not a string.
//
// html/template invokes the method at execute time, which gonertia reaches
// only after it has resolved the props — and resolving the catalogue prop is
// what settles the locale. A string captured when the middleware ran would be
// the pre-authentication answer, so <html lang> would disagree with the body.
func TestLangReadsTheLocaleWhenCalled(t *testing.T) {
	cat := catalog(t)

	// A request that resolves to English up front, with a stored preference
	// for French that is only readable later — exactly the auth ordering.
	translator := cat.Deferred(cat.Negotiator().Default(), func() (string, bool) {
		return "fr", true
	})
	ctx := i18n.With(context.Background(), translator)
	lang := i18ninertia.LangFrom(ctx)

	if got := lang.Lang(); got != "fr" {
		t.Errorf("Lang() = %q, want the late French", got)
	}
	if got := lang.Dir(); got != "ltr" {
		t.Errorf("Dir() = %q", got)
	}
}

func TestDirComesFromTheScript(t *testing.T) {
	cat := catalog(t)
	if got := i18ninertia.PayloadFor(cat.In("ar")).Dir; got != "rtl" {
		t.Errorf("Arabic Dir = %q, want rtl", got)
	}
	if got := i18ninertia.PayloadFor(cat.In("en")).Dir; got != "ltr" {
		t.Errorf("English Dir = %q, want ltr", got)
	}
}

// TestLangIsNilSafe: the template is executed whether or not the middleware
// ran, and a missing attribute beats a 500.
func TestLangIsNilSafe(t *testing.T) {
	var lang *i18ninertia.Lang
	if got := lang.Lang(); got != "" {
		t.Errorf("Lang() on a nil = %q", got)
	}
	if got := lang.Dir(); got != "ltr" {
		t.Errorf("Dir() on a nil = %q, want the harmless default", got)
	}
}

// TestTheTranslatorIsNeverNil: a page in the default language beats a page
// that panicked.
func TestTheTranslatorIsNeverNil(t *testing.T) {
	if got := i18ninertia.TranslatorFrom(context.Background()); got == nil {
		t.Fatal("TranslatorFrom returned nil for a context carrying nothing")
	}
	//nolint:staticcheck // a nil context is what a misuse looks like
	if got := i18ninertia.TranslatorFrom(nil); got == nil {
		t.Fatal("TranslatorFrom returned nil for a nil context")
	}
}
