package i18ntest_test

import (
	"testing"

	"github.com/lemmego/i18n"
	"github.com/lemmego/i18n/i18ntest"
)

func TestCatalogHoldsWhatItWasGivenAndTheFrameworksEnglishUnderneath(t *testing.T) {
	cat := i18ntest.Catalog(t, "en", map[string]any{
		"feed.empty": "Nothing here yet",
		"feed.posts": map[string]string{"one": ":count post", "other": ":count posts"},
	})
	translator := cat.In("en")

	if got := translator.Get("feed.empty"); got != "Nothing here yet" {
		t.Errorf("got %q", got)
	}
	if got := translator.Choice("feed.posts", 1); got != "1 post" {
		t.Errorf("got %q", got)
	}
	if got := translator.Get("validation.required"); got != "This field is required" {
		t.Errorf("got %q; a harness should give a test what an application gets, and "+
			"the framework's own English is part of that", got)
	}
}

func TestMultiExercisesTheFallback(t *testing.T) {
	cat := i18ntest.Multi(t, []string{"en", "fr"}, map[string]map[string]any{
		"en": {"a.b": "English", "a.c": "Only English"},
		"fr": {"a.b": "Français"},
	})
	translator := cat.In("fr")

	if got := translator.Get("a.b"); got != "Français" {
		t.Errorf("got %q", got)
	}
	if got := translator.Get("a.c"); got != "Only English" {
		t.Errorf("got %q", got)
	}
}

func TestAssertNoMissesCatchesAMissingKey(t *testing.T) {
	cat := i18ntest.Catalog(t, "en", nil)
	cat.In("en").Get("page.title")

	fake := &recordingT{}
	i18ntest.AssertNoMisses(fake, cat)
	if fake.errors == 0 {
		t.Error("AssertNoMisses passed a catalogue that was asked for a key it does " +
			"not have, so it would never catch an untranslated page")
	}
}

func TestAssertNoMissesPassesACompletePage(t *testing.T) {
	cat := i18ntest.Catalog(t, "en", map[string]any{"page.title": "Home"})
	cat.In("en").Get("page.title")

	fake := &recordingT{}
	i18ntest.AssertNoMisses(fake, cat)
	if fake.errors != 0 {
		t.Error("AssertNoMisses failed a page whose every key resolved")
	}
}

func TestAssertCompleteNamesWhatIsUntranslated(t *testing.T) {
	cat := i18ntest.Multi(t, []string{"en", "fr"}, map[string]map[string]any{
		"en": {"a.b": "English", "a.c": "Only English"},
		"fr": {"a.b": "Français"},
	})

	fake := &recordingT{}
	i18ntest.AssertComplete(fake, cat, "fr")
	if fake.errors != 1 {
		t.Errorf("recorded %d errors, want 1 naming a.c", fake.errors)
	}
}

func TestAddMessagesRefusesAPluralSetAFileWouldHaveBeenRefusedFor(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	// No "other", which the loader refuses, so building one in memory must be
	// refused too — otherwise a test could assert against a shape no real
	// catalogue could hold.
	err = cat.AddMessages("en", map[string]any{
		"x.y": map[string]string{"one": "a", "few": "b"},
	})
	if err == nil {
		t.Error("a plural set with no \"other\" form was accepted in memory while a " +
			"file holding the same thing is refused")
	}

	if err := cat.AddMessages("en", map[string]any{
		"x.z": map[string]string{"singular": "a", "other": "b"},
	}); err == nil {
		t.Error("an invented plural form name was accepted; the browser can only " +
			"produce the six CLDR categories")
	}

	if err := cat.AddMessages("fr", map[string]any{"a": "b"}); err == nil {
		t.Error("messages were accepted for an unsupported locale")
	}
}

// recordingT counts failures so a helper that is supposed to fail can be
// tested without failing the test that is testing it.
type recordingT struct {
	testing.T
	errors int
}

func (r *recordingT) Helper()                        {}
func (r *recordingT) Errorf(string, ...any)          { r.errors++ }
func (r *recordingT) Fatalf(format string, a ...any) { r.errors++ }
