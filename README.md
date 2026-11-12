# lemmego/i18n

Translation and localization for Lemmego. Laravel-shaped where Laravel is
right, and deliberately different in three places where it is not.

```go
cat, err := i18n.LoadDir("resources/lang", "en", "fr", "ru")

t := cat.In("fr")
t.Get("auth.failed")                              // "Ces identifiants ne correspondent pas."
t.Get("mail.hello", i18n.R{"name": "Ada"})        // "Bonjour, Ada"
t.Choice("feed.posts", 3)                         // "3 publications" — :count is bound for you
```

## The catalogue

One directory per locale, one JSON file per group, and the key is the file
layout:

```
resources/lang/
  en/validation.json            -> validation.required, validation.min.string
  en/auth.json                  -> auth.failed
  fr/validation.json
  vendor/authz/fr/errors.json   -> authz::errors.forbidden
```

```json
{
  "required": "This field is required",
  "min": { "string": "This field must be at least :min characters" },
  "posts": { "=0": "no posts yet", "one": ":count post", "other": ":count posts" }
}
```

A nested object is a group of messages. An object whose keys are all plural
forms is one plural message. An object that **mixes** the two is a load error,
because a JSON object is genuinely ambiguous between them and guessing is how
one message quietly disappears in one language. Plural form names are
therefore reserved everywhere: this module's own catalogue was refused on its
first load for using `other` as an ordinary sub-key of `active_url`.

Loading is eager, covers every locale, and fails the boot on a malformed file.
A thousand messages is tens of kilobytes and parses in milliseconds; in
exchange a broken file is found by whoever broke it rather than by the first
reader of a language nobody on the team reads. A **missing key at runtime**,
by contrast, returns the key and is counted — a page with `profile.header` in
it is legible and obviously wrong, where an error would be no page at all.

Read the counts with `Catalog.Misses()`, or assert on them:

```go
i18ntest.AssertNoMisses(t, cat)   // after rendering a page
i18ntest.AssertComplete(t, cat, "fr")
```

`Catalog.Diff(a, b)` is the same thing without a test, and it is exported
rather than enforced at load time because whether an unfinished translation
should block a deploy is the application's call.

## Placeholders

`:name`, as in Laravel. Two properties are load-bearing and both are pinned by
tests on each side of the wire:

- **The longest placeholder wins.** Go's `strings.NewReplacer` prefers the
  first pair in argument order and a JavaScript regex alternation prefers the
  first branch, where the PHP `strtr` this syntax comes from prefers the
  longest. Unsorted, `one of :values` renders as the value of `:value` with a
  stray `s` after it — and both placeholders are in this module's own
  validation catalogue.
- **A replacement value is never rescanned.** Substituting a string somebody
  else chose is therefore safe. The case that exists today is the multi-file
  upload message, which carries a client-supplied filename: a file called
  `:message.txt` stays literal.

Laravel's `:NAME` and `:Name` auto-capitalisation is **not** implemented. One
rule, and the same rule in the browser.

## Plurals

CLDR categories by name — `zero`, `one`, `two`, `few`, `many`, `other` —
selected with `golang.org/x/text/feature/plural`, plus ICU-style `"=0"` exact
counts checked first.

This is the one place the design departs from Laravel on purpose. Laravel
picks a pipe-separated segment by index, from a switch whose own docblock says
the rules are *"derived from code of the Zend Framework (2010-09-25)"* —
pre-CLDR, matched by exact equality on the whole locale string, so `en-US`
with a hyphen falls through to the default. And `MessageSelector::choose` ends:

```php
if (count($segments) === 1 || ! isset($segments[$pluralIndex])) {
    return $segments[0];
}
```

So Russian with two segments and five apples computes index 2, finds nothing,
and returns **the singular**, silently. Names fix that twice: a translator
opening `ru/feed.json` sees which forms their language needs because the keys
say so, and a form nobody wrote degrades to `other` — the plural — never to
the singular.

Laravel's `[1,19]` / `[20,*]` ranges are dropped. They overlap confusingly
with the categories, and Laravel's own parser is looser than its
documentation: `{1]` parses and `[1, 19]` with a space does not.

`Choice` takes an `int`, not a float, and that is what makes the browser half
provably equivalent: with the four fractional CLDR operands all zero,
`plural.Cardinal.MatchPlural` and `Intl.PluralRules.select` are reading the
same data with the same inputs. `testdata/plural_cases.json` is read by a Go
test **and** by `js/i18n.test.mjs`, so a divergence is a red test rather than
a bug report written in Arabic. It covers English, French (`one` for both 0
and 1 — the row that catches a hand-rolled `n === 1`), Russian, Arabic,
Japanese, Welsh and negative counts.

## Fallback, in three tiers

1. the requested locale
2. the default locale, which is the **first** one passed to `LoadDir` —
   `golang.org/x/text` defines the first tag given to a matcher as its
   fallback, so there is one rule rather than two
3. this module's own English, embedded, which is why validation reads as
   sentences in a project that has published no catalogue at all

There is no walk from `pt-BR` up to `pt`. A parent locale is a guess about
which messages are shared; a project that wants it lists both.

When tier 3 answers, the plural rules used are **English** rather than the
application's default, because the rules have to match the language of the
text they are selecting from.

## Locale resolution

A request's translator settles its locale on **first use**, not on
construction:

```go
t := cat.Deferred(initial, func() (string, bool) { return viewer.Locale, true })
```

The negotiating middleware wraps the mux, so it runs before any route's own
authentication handler — there is no signed-in reader to ask yet. The late
hook is called from the first translation instead, which is after the handler.
Frozen afterwards, deliberately: a translator that kept answering the current
value would render the top of a page in one language and the bottom in
another, which is harder to notice than a whole page in the wrong language.
`Prefer` after that returns `ErrLocaleAlreadyUsed` rather than silently doing
nothing.

The freeze is mutex-guarded because the Inertia renderer resolves every page
prop in its own goroutine, so two lazy props that both translate race by
construction.

`Negotiator` handles the matching. `FromHeader` parses `Accept-Language`
including its `q` weights; `Lookup` matches a single value such as a cookie.
Both report `false` when nothing matches, so a caller falls through to its
next source instead of stopping at a value that was never honoured. The
**index** decides the locale, never the returned tag: `Match` may hand back a
tag that is not one of the supported ones, because it carries over settings
from what the reader asked for.

## Module-shipped strings

A module embeds its own catalogue and mounts it under a namespace:

```go
//go:embed lang
var catalogFS embed.FS

cat.Mount("authz", catalogFS, "lang")   // authz::errors.forbidden
```

An application overrides any of them at
`resources/lang/vendor/authz/<locale>/errors.json`, which needs no new command:
the module's provider returns those files from `AddPublishables`, so
`lemmego run publish --tags=i18n` lays them out and `api/app/publish.go`
already refuses to overwrite an edited one. Overriding one message keeps the
rest, and **load order does not matter** — the two live in separate maps
consulted in order rather than one map merged on load.

## The browser half

`js/i18n.mjs` applies the same two rules. Vendor it into an application
(`lemmego/broadcast` ships its client the same way) and feed it the `i18n`
page prop:

```js
const { t, choice, locale, dir } = createTranslator(page.props.i18n);
```

It is plain JavaScript with a hand-written `i18n.d.mts`, so `node --test` runs
its tests with no transpiler and no devDependency — in a repository that has
no JavaScript test setup at all. `go test` runs them too, through
`js_parity_test.go`, which skips when Node is absent and refuses to pass if
the runner reports no tests.

It exports a factory and holds no module-level state, because the server-side
renderer is one Node process rendering pages for everybody: a shared store
would serve one reader's language to another.

## What this version does not do

- **No number, currency or date formatting.** `x/text/message`'s own
  documentation says it does not format `time.Time`, so a module claiming to
  localize everything would be wrong on the day it shipped — while the
  browser's `Intl.DateTimeFormat` is already correct and free. Those packages
  are denied **by name** in `deps_test.go`, so reversing the decision takes a
  deliberate commit rather than an accidental import.
- **No string extraction tool.** Parsing TSX accurately enough is a parser
  project. The miss recorder is the answer instead: an untranslated page
  reports itself.
- **No locale-prefixed URLs.** A `/{locale}/` stripper has to run before
  pattern matching, which puts it in a chain with the canonical-URL redirects,
  and every ordering there is wrong in a different way — the canonical
  redirect either drops the prefix into a browser-cached 301 or never sees the
  path at all. A query parameter and a cookie issue no redirects.
- **No `:NAME` capitalisation**, and **no pipe-separated plurals**. See above.

## Dependencies

The root package is the standard library plus two trees of
`golang.org/x/text` — `language` and `feature/plural`. 87 packages in the
whole closure, three of them outside the standard library. `deps_test.go`
fails if that changes, and names the two places a framework dependency is
allowed to go instead:

- `i18n/provider` — the `app.Provider` and the negotiating middleware
- `i18n/i18ninertia` — the shared page prop

The second cannot live in `api` at all: `lemmego/inertia` requires
`lemmego/api`, so that edge only goes one way. And `api` does not depend on
this module — `api/app` declares a two-method `Translator` interface that
`*i18n.Translator` satisfies structurally, which keeps the CLDR tables out of
every binary that never translates anything and avoids a module cycle.

## Testing

```bash
go test ./...
go test -race ./...
node --test js/i18n.test.mjs
GOWORK=off go mod tidy && GOWORK=off go test ./...
```
