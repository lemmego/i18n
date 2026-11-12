import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { createTranslator, interpolate } from "./i18n.mjs";

const fixturePath = fileURLToPath(
  new URL("../testdata/plural_cases.json", import.meta.url),
);
const fixtures = JSON.parse(readFileSync(fixturePath, "utf8"));

/**
 * The same file the Go test reads.
 *
 * One fixture, two readers, deliberately: the Go side selects a plural form
 * from x/text's CLDR tables and this side from Intl.PluralRules, and the only
 * thing keeping those two answers equal is that both are checked against the
 * same expectations. A divergence is then a red test rather than a bug report
 * written in Arabic.
 */
test("plurals match the shared fixture", () => {
  assert.ok(fixtures.length > 0, "the shared fixture is empty, so this proves nothing");

  for (const fixture of fixtures) {
    const translator = createTranslator({
      locale: fixture.locale,
      fallback: "en",
      dir: "ltr",
      messages: { "test.items": fixture.forms },
    });

    for (const { n, want } of fixture.cases) {
      assert.equal(
        translator.choice("test.items", n),
        want,
        `${fixture.locale} n=${n}: ${fixture.why}`,
      );
    }
  }
});

test("every fixture locale is one Intl knows about", () => {
  // Guards the guard: if Intl silently fell back to English for a locale, the
  // test above would still pass and would be asserting nothing about that
  // language.
  for (const fixture of fixtures) {
    const resolved = new Intl.PluralRules(fixture.locale).resolvedOptions().locale;
    assert.equal(
      resolved.split("-")[0],
      fixture.locale.split("-")[0],
      `Intl resolved ${fixture.locale} to ${resolved}, so this runtime is not ` +
        `applying that language's rules and the fixture row is vacuous`,
    );
  }
});

test("an unwritten form degrades to the general one, never the singular", () => {
  const translator = createTranslator({
    locale: "ru",
    fallback: "en",
    dir: "ltr",
    messages: { "test.apples": { one: "одно яблоко", other: "много яблок" } },
  });
  assert.equal(translator.choice("test.apples", 5), "много яблок");
});

test("an exact count beats the category", () => {
  const translator = createTranslator({
    locale: "en",
    fallback: "en",
    dir: "ltr",
    messages: { "feed.posts": { "=0": "no posts yet", one: ":count post", other: ":count posts" } },
  });
  assert.equal(translator.choice("feed.posts", 0), "no posts yet");
  assert.equal(translator.choice("feed.posts", 1), "1 post");
  assert.equal(translator.choice("feed.posts", 9), "9 posts");
});

test("a longer placeholder wins over a shorter prefix", () => {
  // The JavaScript-specific half of the same trap the Go side has: a regex
  // alternation takes the first branch that matches at a position, so the
  // keys have to be sorted longest-first.
  assert.equal(
    interpolate("one of :values, not :value", { value: "red", values: "green, blue" }),
    "one of green, blue, not red",
  );
});

test("a replacement containing a colon is not reinterpreted", () => {
  assert.equal(
    interpolate(":file — :message", {
      file: ":message.txt",
      message: "This file is not an image",
    }),
    ":message.txt — This file is not an image",
  );
});

test("an unknown placeholder is left verbatim", () => {
  assert.equal(interpolate("Hello, :nmae", { name: "Ada" }), "Hello, :nmae");
});

test("adjacent placeholders render", () => {
  // The dimensions message is ":widthx:height" — the one place in the shipped
  // catalogue where a placeholder is followed immediately by a character that
  // could have been part of its name.
  assert.equal(
    interpolate("Image dimensions must be :widthx:height", { width: 100, height: 50 }),
    "Image dimensions must be 100x50",
  );
});

test("count is bound unless the caller supplied it", () => {
  const translator = createTranslator({
    locale: "en",
    fallback: "en",
    dir: "ltr",
    messages: { "x.y": { one: ":count thing", other: ":count things" } },
  });
  assert.equal(translator.choice("x.y", 3), "3 things");
  assert.equal(translator.choice("x.y", 3, { count: "three" }), "three things");
});

test("a missing key is returned and reported once", () => {
  const missed = [];
  const translator = createTranslator(
    { locale: "en", fallback: "en", dir: "ltr", messages: {} },
    (key) => missed.push(key),
  );
  assert.equal(translator.t("nope.at.all"), "nope.at.all");
  translator.t("nope.at.all");
  assert.deepEqual(missed, ["nope.at.all"]);
});

test("an empty payload does not throw", () => {
  // The Once prop is absent from page.props after a partial reload, so this
  // shape really does reach the browser.
  const translator = createTranslator(undefined);
  assert.equal(translator.t("a.b"), "a.b");
  assert.equal(translator.locale, "en");
  assert.equal(translator.dir, "ltr");
});
