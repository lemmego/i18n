package i18n_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/lemmego/i18n"
)

// catalogWith builds a one-locale catalogue from an in-memory file, so a test
// states the messages it cares about and nothing else.
func catalogWith(t *testing.T, locale string, group map[string]any) *i18n.Catalog {
	t.Helper()
	return catalogFrom(t, []string{locale}, map[string]map[string]any{
		locale + "/test.json": group,
	})
}

func catalogFrom(t *testing.T, supported []string, files map[string]map[string]any) *i18n.Catalog {
	t.Helper()
	fsys := fstest.MapFS{}
	for path, content := range files {
		data, err := json.Marshal(content)
		if err != nil {
			t.Fatalf("encoding %s: %v", path, err)
		}
		fsys[path] = &fstest.MapFile{Data: data}
	}
	cat, err := i18n.LoadFS(fsys, ".", supported...)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	return cat
}

func TestPlaceholdersAreSubstituted(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{
		"hello": "Hello, :name",
	})
	if got := cat.In("en").Get("test.hello", i18n.R{"name": "Ada"}); got != "Hello, Ada" {
		t.Errorf("got %q", got)
	}
}

// TestALongerPlaceholderWinsOverAShorterPrefix is the Go-specific trap.
//
// strings.NewReplacer prefers the first pair in argument order that matches at
// a position, where the PHP strtr this syntax comes from prefers the longest.
// So an unsorted replacer handed "value" before "values" renders ":values" as
// the value of :value with a stray "s" after it. Both placeholders are in this
// framework's own validation catalogue.
func TestALongerPlaceholderWinsOverAShorterPrefix(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{
		"pick": "one of :values, not :value",
	})
	got := cat.In("en").Get("test.pick", i18n.R{
		"value":  "red",
		"values": "green, blue",
	})
	if want := "one of green, blue, not red"; got != want {
		t.Errorf("got %q, want %q\nthe replacer's pairs are not sorted longest-first", got, want)
	}
}

// TestAReplacementContainingAColonIsNotReinterpreted pins the other half of
// the substituter's contract.
//
// A replacement value is never rescanned, which is what makes it safe to
// substitute a string somebody else chose. The case that exists today is the
// multi-file upload message, which carries a client-supplied filename.
func TestAReplacementContainingAColonIsNotReinterpreted(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{
		"upload": ":file — :message",
	})
	got := cat.In("en").Get("test.upload", i18n.R{
		"file":    ":message.txt",
		"message": "This file is not an image",
	})
	if want := ":message.txt — This file is not an image"; got != want {
		t.Errorf("got %q, want %q\na replacement value was reinterpreted as a placeholder", got, want)
	}
}

func TestAnUnknownPlaceholderIsLeftVerbatim(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{"oops": "Hello, :nmae"})
	if got := cat.In("en").Get("test.oops", i18n.R{"name": "Ada"}); got != "Hello, :nmae" {
		t.Errorf("got %q; a misspelled placeholder should stay visible rather than "+
			"being blanked, because the page is then its own bug report", got)
	}
}

// TestAdjacentPlaceholdersRender covers the dimensions message, which is
// ":widthx:height" — the one place in the shipped catalogue where a
// placeholder is followed immediately by a literal character that could have
// been part of its name.
func TestAdjacentPlaceholdersRender(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	got := cat.In("en").Get("validation.dimensions", i18n.R{"width": 100, "height": 50})
	if want := "Image dimensions must be 100x50"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAListOfValuesReadsAsASentence(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	got := cat.In("en").Get("validation.in", i18n.R{"values": []string{"red", "green", "blue"}})
	if !strings.HasSuffix(got, "red, green, blue") {
		t.Errorf("got %q; a []string must join with \", \" rather than render as Go's "+
			"slice syntax in the middle of a sentence", got)
	}
}

func TestAMissingKeyIsReturnedAndCounted(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{"known": "fine"})
	translator := cat.In("en")

	if got := translator.Get("test.unknown"); got != "test.unknown" {
		t.Errorf("got %q, want the key back", got)
	}
	translator.Get("test.unknown")

	misses := cat.Misses()
	if misses["test.unknown"] != 2 {
		t.Errorf("misses = %v; a missing key must be counted so an untranslated page "+
			"reports itself", misses)
	}
	if _, counted := misses["test.known"]; counted {
		t.Error("a key that was found was counted as missing")
	}
}

func TestAskingWhetherAKeyExistsDoesNotCountAsAMiss(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{"known": "fine"})
	translator := cat.In("en")

	if translator.Has("test.nope") {
		t.Error("Has reported a key that is not there")
	}
	if len(cat.Misses()) != 0 {
		t.Errorf("misses = %v; asking is not needing, so Has must not record one", cat.Misses())
	}
}

// TestTheFrameworksOwnEnglishIsTheFloor is what makes this non-breaking: a
// project that publishes no catalogue still reads its validation errors as
// sentences.
func TestTheFrameworksOwnEnglishIsTheFloor(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.In("en").Get("validation.required"); got != "This field is required" {
		t.Errorf("got %q; the embedded English should answer with nothing published", got)
	}
}

// TestTheFloorIsEnglishEvenUnderAnotherDefault pairs the fallback text with
// the fallback's own plural rules. A Russian application falling back to an
// English sentence must not then ask Russian rules which of its four forms to
// use.
func TestTheFloorIsEnglishEvenUnderAnotherDefault(t *testing.T) {
	cat, err := i18n.NewCatalog("ru")
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.In("ru").Choice("auth.throttle", 5); !strings.Contains(got, "5 seconds") {
		t.Errorf("got %q; an English fallback must be pluralised by English rules", got)
	}
}

func TestAnApplicationOverridesOneMessageAndKeepsTheRest(t *testing.T) {
	cat := catalogFrom(t, []string{"en"}, map[string]map[string]any{
		"en/validation.json": {"required": "You have to fill this in"},
	})
	translator := cat.In("en")

	if got := translator.Get("validation.required"); got != "You have to fill this in" {
		t.Errorf("the override did not win: %q", got)
	}
	if got := translator.Get("validation.email"); got != "This field must be a valid email address" {
		t.Errorf("overriding one message lost another: %q", got)
	}
}

func TestAModuleMountsItsOwnMessagesAndTheAppOverridesThem(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}

	moduleFS := fstest.MapFS{
		"lang/en/errors.json": &fstest.MapFile{
			Data: []byte(`{"forbidden":"You may not do that","other":"Something else"}`),
		},
	}
	if err := cat.Mount("authz", moduleFS, "lang"); err != nil {
		t.Fatalf("mounting: %v", err)
	}
	if got := cat.In("en").Get("authz::errors.forbidden"); got != "You may not do that" {
		t.Errorf("the module's own message did not resolve: %q", got)
	}

	// The application's override, read from resources/lang/vendor/authz/en/.
	// Loaded after the mount here, and the test below loads it before — the
	// point being that order does not matter.
	appFS := fstest.MapFS{
		"vendor/authz/en/errors.json": &fstest.MapFile{
			Data: []byte(`{"forbidden":"Not allowed"}`),
		},
	}
	if err := cat.LoadFS(appFS, "."); err != nil {
		t.Fatalf("loading the override: %v", err)
	}
	translator := cat.In("en")
	if got := translator.Get("authz::errors.forbidden"); got != "Not allowed" {
		t.Errorf("the application's override did not win: %q", got)
	}
	if got := translator.Get("authz::errors.other"); got != "Something else" {
		t.Errorf("overriding one of a module's messages lost another: %q", got)
	}
}

func TestAnOverrideLoadedBeforeTheMountStillWins(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	appFS := fstest.MapFS{
		"vendor/authz/en/errors.json": &fstest.MapFile{
			Data: []byte(`{"forbidden":"Not allowed"}`),
		},
	}
	if err := cat.LoadFS(appFS, "."); err != nil {
		t.Fatal(err)
	}
	moduleFS := fstest.MapFS{
		"lang/en/errors.json": &fstest.MapFile{
			Data: []byte(`{"forbidden":"You may not do that"}`),
		},
	}
	if err := cat.Mount("authz", moduleFS, "lang"); err != nil {
		t.Fatal(err)
	}
	if got := cat.In("en").Get("authz::errors.forbidden"); got != "Not allowed" {
		t.Errorf("got %q; load order must not decide which of the two wins", got)
	}
}

func TestAKeyMissingInOneLocaleFallsBackToTheDefault(t *testing.T) {
	cat := catalogFrom(t, []string{"en", "fr"}, map[string]map[string]any{
		"en/test.json": {"hello": "Hello", "bye": "Goodbye"},
		"fr/test.json": {"hello": "Bonjour"},
	})
	translator := cat.In("fr")

	if got := translator.Get("test.hello"); got != "Bonjour" {
		t.Errorf("got %q", got)
	}
	if got := translator.Get("test.bye"); got != "Goodbye" {
		t.Errorf("got %q; an untranslated key should fall back to the default locale", got)
	}
}

func TestDiffNamesWhatOneLocaleIsMissing(t *testing.T) {
	cat := catalogFrom(t, []string{"en", "fr"}, map[string]map[string]any{
		"en/test.json": {"hello": "Hello", "bye": "Goodbye"},
		"fr/test.json": {"hello": "Bonjour"},
	})
	missing := cat.Diff("en", "fr")
	if len(missing) != 1 || missing[0] != "test.bye" {
		t.Errorf("Diff = %v, want [test.bye]", missing)
	}
}

func TestExportCarriesTheDefaultLocaleAsWell(t *testing.T) {
	cat := catalogFrom(t, []string{"en", "fr"}, map[string]map[string]any{
		"en/test.json": {"hello": "Hello", "bye": "Goodbye"},
		"fr/test.json": {"hello": "Bonjour"},
	})
	exported := cat.Export("fr")

	if exported["test.hello"] != "Bonjour" {
		t.Errorf("test.hello = %v, want the French", exported["test.hello"])
	}
	if exported["test.bye"] != "Goodbye" {
		t.Errorf("test.bye = %v; the browser cannot fall back to a catalogue it was "+
			"never sent, so the default locale's text has to travel with it",
			exported["test.bye"])
	}
}

func TestDirIsComputedFromTheScript(t *testing.T) {
	cat, err := i18n.NewCatalog("en", "ar", "fa", "he")
	if err != nil {
		t.Fatal(err)
	}
	for locale, want := range map[string]string{
		"en": "ltr",
		"ar": "rtl",
		"fa": "rtl",
		"he": "rtl",
	} {
		if got := cat.In(locale).Dir(); got != want {
			t.Errorf("Dir(%s) = %q, want %q", locale, got, want)
		}
	}
}

// TestTheLocaleIsSettledOnFirstUseAndThenFrozen is the rule the whole lazy
// design rests on.
func TestTheLocaleIsSettledOnFirstUseAndThenFrozen(t *testing.T) {
	cat := catalogFrom(t, []string{"en", "fr"}, map[string]map[string]any{
		"en/test.json": {"hello": "Hello"},
		"fr/test.json": {"hello": "Bonjour"},
	})

	// The middleware settled English; the signed-in reader prefers French,
	// and is only knowable after the route's own auth handler has run.
	translator := cat.Deferred(cat.Negotiator().Default(), func() (string, bool) {
		return "fr", true
	})

	if got := translator.Get("test.hello"); got != "Bonjour" {
		t.Fatalf("got %q; the late source must be consulted on first use, because the "+
			"negotiating middleware runs before the route's auth handler", got)
	}
	if got := translator.Locale(); got != "fr" {
		t.Errorf("locale = %q after the first translation", got)
	}
	if res := translator.Resolution(); res.Source != i18n.SourceUser {
		t.Errorf("source = %v, want user", res.Source)
	}
}

func TestTheLateSourceIsConsultedOnlyOnce(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{"hello": "Hello"})

	var calls int
	translator := cat.Deferred(cat.Negotiator().Default(), func() (string, bool) {
		calls++
		return "en", true
	})
	translator.Get("test.hello")
	translator.Get("test.hello")
	translator.Locale()

	if calls != 1 {
		t.Errorf("the late source ran %d times; it reads a signed-in reader's stored "+
			"preference, which may be a database query", calls)
	}
}

func TestPreferringALocaleAfterTranslatingIsRefused(t *testing.T) {
	cat := catalogFrom(t, []string{"en", "fr"}, map[string]map[string]any{
		"en/test.json": {"hello": "Hello"},
		"fr/test.json": {"hello": "Bonjour"},
	})
	translator := cat.Deferred(cat.Negotiator().Default(), nil)

	if got := translator.Get("test.hello"); got != "Hello" {
		t.Fatalf("got %q", got)
	}
	if err := translator.Prefer("fr"); err != i18n.ErrLocaleAlreadyUsed {
		t.Errorf("Prefer after a translation returned %v, want ErrLocaleAlreadyUsed; "+
			"silently doing nothing would render the top of a page in one language "+
			"and the bottom in another", err)
	}
	if got := translator.Get("test.hello"); got != "Hello" {
		t.Errorf("got %q; the refused Prefer changed the locale anyway", got)
	}
}

func TestPreferringAnUnsupportedLocaleIsRefused(t *testing.T) {
	cat := catalogWith(t, "en", map[string]any{"hello": "Hello"})
	translator := cat.Deferred(cat.Negotiator().Default(), nil)

	err := translator.Prefer("xx-YY")
	var unsupported *i18n.UnsupportedLocaleError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Prefer(\"xx-YY\") returned %v, want an UnsupportedLocaleError", err)
	}
	if !strings.Contains(err.Error(), "en") {
		t.Errorf("the error does not name what is supported: %v", err)
	}
}

// TestConcurrentTranslationsAgree exists because the Inertia renderer resolves
// every page prop in its own goroutine, so two lazy props that both translate
// race here by construction. Run with -race.
func TestConcurrentTranslationsAgree(t *testing.T) {
	cat := catalogFrom(t, []string{"en", "fr"}, map[string]map[string]any{
		"en/test.json": {"hello": "Hello"},
		"fr/test.json": {"hello": "Bonjour"},
	})
	translator := cat.Deferred(cat.Negotiator().Default(), func() (string, bool) {
		return "fr", true
	})

	const readers = 32
	results := make([]string, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = translator.Get("test.hello")
		}()
	}
	wg.Wait()

	for i, got := range results {
		if got != results[0] {
			t.Fatalf("reader %d got %q and reader 0 got %q; one request must have one "+
				"locale", i, got, results[0])
		}
	}
	if results[0] != "Bonjour" {
		t.Errorf("got %q, want the late source to have won", results[0])
	}
}
