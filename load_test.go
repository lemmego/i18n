package i18n_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lemmego/i18n"
)

func loadErr(t *testing.T, supported []string, files map[string]string) error {
	t.Helper()
	fsys := fstest.MapFS{}
	for path, content := range files {
		fsys[path] = &fstest.MapFile{Data: []byte(content)}
	}
	_, err := i18n.LoadFS(fsys, ".", supported...)
	return err
}

func TestMalformedCatalogues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    map[string]string
		contains []string
	}{
		{
			name:  "invalid JSON reports where",
			files: map[string]string{"en/test.json": "{\n  \"a\": \"one\",\n  \"b\": oops\n}"},
			// The offset is the whole point: a boot failure somebody can fix
			// rather than one they have to bisect.
			contains: []string{"line 3", "column"},
		},
		{
			name:     "a non-string leaf is refused",
			files:    map[string]string{"en/test.json": `{"count": 7}`},
			contains: []string{"test.count", "a number", "must be a string"},
		},
		{
			name:     "an array is refused",
			files:    map[string]string{"en/test.json": `{"items": ["a","b"]}`},
			contains: []string{"test.items", "an array"},
		},
		{
			// The ambiguity this module refuses to guess at. The alternative
			// is one message quietly disappearing in one language.
			name:     "a plural set mixed with ordinary keys is refused",
			files:    map[string]string{"en/test.json": `{"x": {"one": "a", "title": "b"}}`},
			contains: []string{"test.x", "mixes plural forms", "one", "title"},
		},
		{
			name:     "a plural set without other is refused",
			files:    map[string]string{"en/test.json": `{"x": {"one": "a", "few": "b"}}`},
			contains: []string{"test.x", `no "other" form`},
		},
		{
			name:     "a non-string plural form is refused",
			files:    map[string]string{"en/test.json": `{"x": {"one": "a", "other": 2}}`},
			contains: []string{"test.x.other", "must be a string"},
		},
		{
			name:     "an empty object is refused",
			files:    map[string]string{"en/test.json": `{"x": {}}`},
			contains: []string{"test.x", "empty object"},
		},
		{
			name:     "a file that is not an object is refused",
			files:    map[string]string{"en/test.json": `["a","b"]`},
			contains: []string{"must hold a JSON object"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadErr(t, []string{"en"}, tc.files)
			if err == nil {
				t.Fatal("loaded without error")
			}
			for _, want := range tc.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not mention %q:\n%v", want, err)
				}
			}
		})
	}
}

// TestALocaleDirectoryMustBeNamedCanonically is the bug that passes on every
// macOS laptop and fails only in the Linux image.
//
// "pt-br" parses to the tag pt-BR, so a case-insensitive filesystem finds the
// directory either way and Linux does not: the locale works in development
// and silently serves the default in production.
func TestALocaleDirectoryMustBeNamedCanonically(t *testing.T) {
	err := loadErr(t, []string{"pt-BR"}, map[string]string{
		"pt-br/test.json": `{"hello":"Olá"}`,
	})
	if err == nil {
		t.Fatal("a lower-cased locale directory loaded without complaint")
	}
	if !strings.Contains(err.Error(), "pt-BR") {
		t.Errorf("the error does not name the canonical spelling:\n%v", err)
	}
	if !strings.Contains(err.Error(), "case-insensitive") {
		t.Errorf("the error does not say why this matters, so whoever hits it will "+
			"think the check is pedantic:\n%v", err)
	}
}

// TestADirectoryForAnUnsupportedLocaleIsRefused rather than skipped: a
// skipped directory is a translated locale that does nothing, with nothing
// anywhere saying why.
func TestADirectoryForAnUnsupportedLocaleIsRefused(t *testing.T) {
	err := loadErr(t, []string{"en"}, map[string]string{
		"en/test.json": `{"hello":"Hello"}`,
		"de/test.json": `{"hello":"Hallo"}`,
	})
	if err == nil {
		t.Fatal("a directory for an unsupported locale was silently ignored")
	}
	if !strings.Contains(err.Error(), "de") || !strings.Contains(err.Error(), "not a supported locale") {
		t.Errorf("the error does not name the locale and the problem:\n%v", err)
	}
}

func TestAnUncanonicalSupportedLocaleIsRefused(t *testing.T) {
	_, err := i18n.NewCatalog("en", "pt-br")
	if err == nil {
		t.Fatal("a non-canonical supported locale was accepted")
	}
	if !strings.Contains(err.Error(), "pt-BR") {
		t.Errorf("the error does not name the canonical spelling:\n%v", err)
	}
}

func TestNoSupportedLocalesIsRefused(t *testing.T) {
	if _, err := i18n.NewCatalog(); err == nil {
		t.Fatal("a catalogue with no locales was accepted; the first is the default, " +
			"so there is nothing to fall back to")
	}
}

func TestADuplicateSupportedLocaleIsRefused(t *testing.T) {
	if _, err := i18n.NewCatalog("en", "fr", "en"); err == nil {
		t.Fatal("a duplicated locale was accepted")
	}
}

func TestAMissingCatalogueDirectoryIsNotAnError(t *testing.T) {
	// An application that has published nothing still boots, and every key
	// falls through to the framework's own English.
	cat, err := i18n.LoadDir("testdata/does-not-exist", "en")
	if err != nil {
		t.Fatalf("a missing directory should not stop a boot: %v", err)
	}
	if got := cat.In("en").Get("validation.required"); got != "This field is required" {
		t.Errorf("got %q", got)
	}
}

func TestNestedGroupsBecomeDottedKeys(t *testing.T) {
	cat := catalogFrom(t, []string{"en"}, map[string]map[string]any{
		"en/validation.json": {"min": map[string]any{"string": "at least :min characters"}},
	})
	if got := cat.In("en").Get("validation.min.string", i18n.R{"min": 8}); got != "at least 8 characters" {
		t.Errorf("got %q", got)
	}
}

func TestAnUnusableNamespaceIsRefused(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"", "a.b", "a::b"} {
		if err := cat.Mount(ns, fstest.MapFS{}, "lang"); err == nil {
			t.Errorf("Mount(%q) was accepted; the namespace becomes the part of a key "+
				"before \"::\"", ns)
		}
	}
}

// TestTheShippedCatalogueLoads is the test that would have caught the
// framework's own catalogue using "other" as an ordinary sub-key, which it
// did: active_url had other/status/too_large, and the mixed-set rule above
// refused it. Plural form names are reserved everywhere, not only in messages
// that mean to be plural.
func TestTheShippedCatalogueLoads(t *testing.T) {
	keys := i18n.BuiltinKeys()
	if len(keys) < 40 {
		t.Fatalf("the framework's own catalogue has %d keys, which is too few to be "+
			"the real one", len(keys))
	}
	for _, key := range []string{
		"validation.required", "validation.min.string", "validation.max.file",
		"validation.dimensions", "validation.file_prefixed", "http.404", "auth.failed",
	} {
		if _, _, ok := i18n.BuiltinMessage(key); !ok {
			t.Errorf("the shipped catalogue has no %s", key)
		}
	}
}
