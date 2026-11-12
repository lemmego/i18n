package provider_test

import (
	"testing"

	"github.com/lemmego/api/app"
	"github.com/lemmego/i18n"
)

// This package is the only one that imports both api/app and lemmego/i18n,
// which makes it the only place these two can be compared.
//
// They hold the same English twice on purpose: api/app keeps a Go map so a
// validator reads as sentences with no translator registered and no i18n in
// go.mod, and i18n embeds the JSON so a translator can override one message
// and keep the rest. The duplication is what keeps api from depending on
// i18n, and it costs a module cycle to remove. These tests are what keep it
// from being a drift hazard.

func TestEveryBuiltinKeyIsInTheCatalogue(t *testing.T) {
	builtin := app.BuiltinValidationMessages()
	if len(builtin) < 40 {
		t.Fatalf("api/app has %d built-in messages, which is too few to be the real "+
			"set — this test is not reading what it thinks it is", len(builtin))
	}

	for key, want := range builtin {
		text, forms, ok := i18n.BuiltinMessage(key)
		if !ok {
			t.Errorf("api/app has %q and i18n's catalogue does not, so an application "+
				"that publishes the catalogue loses that message", key)
			continue
		}
		if forms != nil {
			t.Errorf("%q is a plural message in i18n's catalogue and a plain string in "+
				"api/app, so the two cannot render the same sentence", key)
			continue
		}
		if text != want {
			t.Errorf("%q differs:\n  api/app: %q\n  i18n:    %q", key, want, text)
		}
	}
}

func TestEveryCatalogueKeyTheValidatorNeedsIsInApi(t *testing.T) {
	// The reverse direction, limited to the groups api renders itself. i18n
	// ships auth, passwords and pagination too, which api has no opinion
	// about.
	builtin := app.BuiltinValidationMessages()
	for _, key := range i18n.BuiltinKeys() {
		if !isApiGroup(key) {
			continue
		}
		if _, ok := builtin[key]; !ok {
			t.Errorf("i18n's catalogue has %q and api/app's floor does not, so an "+
				"application with no translator registered would see the raw key", key)
		}
	}
}

func isApiGroup(key string) bool {
	for _, prefix := range []string{"validation.", "http."} {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// TestTheTwoSubstitutersAgree drives api/app's copy and i18n's over the same
// adversarial table.
//
// Both implement the same two rules — longest placeholder first, and never
// rescan a replacement — and both are about twenty lines, so the risk is not
// that one is wrong today but that one is fixed later and the other is not.
func TestTheTwoSubstitutersAgree(t *testing.T) {
	cat, err := i18n.NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		text string
		repl map[string]any
	}{
		{"a longer placeholder wins", "one of :values, not :value",
			map[string]any{"value": "red", "values": "green, blue"}},
		{"a replacement with a colon is not rescanned", ":file — :message",
			map[string]any{"file": ":message.txt", "message": "not an image"}},
		{"adjacent placeholders", "must be :widthx:height",
			map[string]any{"width": 100, "height": 50}},
		{"an unknown placeholder stays", "Hello, :nmae",
			map[string]any{"name": "Ada"}},
		{"a string slice reads as a sentence", "one of :values",
			map[string]any{"values": []string{"a", "b", "c"}}},
		{"a nil value renders as nothing", "x:y z",
			map[string]any{"y": nil}},
		{"no replacements at all", "nothing to do here", nil},
		{"a value that is a number", "at least :min",
			map[string]any{"min": 8}},
		{"a placeholder repeated twice", ":name and :name again",
			map[string]any{"name": "Ada"}},
		{"three keys sharing a prefix", ":a :ab :abc",
			map[string]any{"a": "1", "ab": "2", "abc": "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// i18n has no exported substituter of its own, so it is driven
			// through a one-message catalogue, which is the same code path a
			// real message takes.
			if err := cat.AddMessages("en", map[string]any{"parity.case": tc.text}); err != nil {
				t.Fatal(err)
			}
			fromI18n := cat.In("en").Get("parity.case", tc.repl)
			fromApi := app.Interpolate(tc.text, tc.repl)

			if fromApi != fromI18n {
				t.Errorf("the two substituters disagree on %q:\n  api/app: %q\n  i18n:    %q",
					tc.text, fromApi, fromI18n)
			}
		})
	}
}

// TestTheCatalogueCoversTheHttpStatuses pairs with the sentinels in
// api/app/errors.go, which are never themselves localised.
func TestTheCatalogueCoversTheHttpStatuses(t *testing.T) {
	for _, status := range []string{"400", "401", "403", "404", "405", "419", "422", "500"} {
		key := "http." + status
		if _, _, ok := i18n.BuiltinMessage(key); !ok {
			t.Errorf("the catalogue has no %s, so that status cannot be translated", key)
		}
	}
}
