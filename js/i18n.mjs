/**
 * The browser half of lemmego/i18n.
 *
 * It reads the payload the i18ninertia package shares as a page prop and
 * applies the same two rules the Go side does: :name substitution with the
 * longest placeholder winning, and plural selection by CLDR category with an
 * exact count taking precedence.
 *
 * # Why this is .mjs and not .ts
 *
 * It is plain JavaScript with JSDoc types and a hand-written i18n.d.mts beside
 * it, so `node --test` runs its tests with no toolchain, no transpiler and no
 * devDependency — on a CI runner that has only Node, and in a repository that
 * has no JavaScript test setup at all. A TypeScript source would need one of
 * those before the first test could run, and the thing being tested is the
 * one piece of logic that has to agree with Go. TypeScript consumers get full
 * typing from the .d.ts, and Vite serves this file unchanged.
 *
 * # Why there is no React in here
 *
 * A context provider belongs with the application, and this file is also
 * imported by the server-side renderer, where there is no document and no
 * single current locale — one Node process renders pages for everybody. So
 * this exports a factory and holds no module-level mutable state: a store
 * shared across concurrent renders would serve one reader's language to
 * another.
 */

/** @typedef {Record<string, string | number>} Replacements */
/** @typedef {Record<string, string | Record<string, string>>} Messages */

/**
 * @typedef {object} Payload
 * @property {string} locale
 * @property {string} fallback
 * @property {"ltr" | "rtl"} dir
 * @property {Messages} messages
 */

const FORMS = ["zero", "one", "two", "few", "many", "other"];

/**
 * createTranslator returns the reading half of a payload.
 *
 * @param {Payload} payload
 * @param {(key: string) => void} [onMiss] called once per missing key, so a
 *   development build can report what a page asked for and did not get. A
 *   no-op in production, like its Go counterpart's miss recorder.
 */
export function createTranslator(payload, onMiss) {
  const messages = (payload && payload.messages) || {};
  const locale = (payload && payload.locale) || "en";
  const dir = (payload && payload.dir) || "ltr";
  const reported = new Set();

  // One PluralRules per translator rather than per call: constructing one is
  // not free, and a page renders hundreds of strings.
  let rules;
  const plural = (n) => {
    if (!rules) {
      try {
        rules = new Intl.PluralRules(locale);
      } catch {
        // An unknown locale is not worth a blank page. English rules are
        // wrong for one language and a thrown exception is wrong for the
        // whole page.
        rules = new Intl.PluralRules("en");
      }
    }
    return rules.select(n);
  };

  const miss = (key) => {
    if (onMiss && !reported.has(key)) {
      reported.add(key);
      onMiss(key);
    }
    return key;
  };

  /**
   * @param {string} key
   * @param {Replacements} [repl]
   * @returns {string}
   */
  function t(key, repl) {
    const entry = messages[key];
    if (typeof entry === "string") return interpolate(entry, repl);
    if (entry && typeof entry === "object") {
      // A plural message asked for without a count: "other" is the most
      // useful single answer, and it is better than the key.
      return interpolate(entry.other ?? firstForm(entry), repl);
    }
    return interpolate(miss(key), repl);
  }

  /**
   * @param {string} key
   * @param {number} n
   * @param {Replacements} [repl]
   * @returns {string}
   */
  function choice(key, n, repl) {
    const values = { count: n, ...(repl || {}) };
    const entry = messages[key];
    if (typeof entry === "string") return interpolate(entry, values);
    if (!entry || typeof entry !== "object") {
      return interpolate(miss(key), values);
    }
    return interpolate(selectForm(entry, plural, n), values);
  }

  return { t, choice, locale, dir };
}

/**
 * selectForm picks one string out of a plural set.
 *
 * Exact counts first, then the CLDR category, then "other". The last step is
 * what keeps this honest in a language with more forms than the translator
 * wrote: a Russian message with only "one" and "other" renders the plural for
 * five, never the singular — which is the Laravel bug this whole design
 * exists to avoid, and it has to be avoided identically on both sides of the
 * wire or the page disagrees with itself.
 *
 * @param {Record<string, string>} forms
 * @param {(n: number) => string} plural
 * @param {number} n
 */
function selectForm(forms, plural, n) {
  const exact = forms["=" + n];
  if (exact !== undefined) return exact;

  // CLDR reads the absolute value: "-1 item" takes the same form as "1 item".
  const category = plural(Math.abs(n));
  if (forms[category] !== undefined) return forms[category];
  if (forms.other !== undefined) return forms.other;
  return firstForm(forms);
}

/** @param {Record<string, string>} forms */
function firstForm(forms) {
  for (const name of FORMS) {
    if (forms[name] !== undefined) return forms[name];
  }
  const keys = Object.keys(forms);
  return keys.length ? forms[keys[0]] : "";
}

/**
 * interpolate substitutes :name placeholders.
 *
 * One pass over the string with an alternation of the known placeholders,
 * sorted longest-first. Both halves of that matter and both mirror the Go
 * side exactly.
 *
 * Longest-first because a JavaScript regex alternation takes the first branch
 * that matches at a position, so ":value" listed before ":values" would
 * render ":values" as the value of :value with a stray "s" after it. Both are
 * in the framework's own validation catalogue.
 *
 * And one pass with a replacer function, because String.replace never
 * rescans what it has just written. That is what makes it safe to substitute
 * a string somebody else chose — today, a client-supplied filename in an
 * upload error. A loop of replaceAll calls would be wrong on both counts.
 *
 * An unknown placeholder is left verbatim: ":nmae" in the page is a bug
 * report, an empty space is a mystery.
 *
 * @param {string} text
 * @param {Replacements} [repl]
 */
export function interpolate(text, repl) {
  if (!repl || typeof text !== "string" || text.indexOf(":") === -1) return text;

  const keys = Object.keys(repl);
  if (keys.length === 0) return text;
  keys.sort((a, b) => b.length - a.length || (a < b ? -1 : a > b ? 1 : 0));

  const pattern = new RegExp(":(" + keys.map(escapeRegExp).join("|") + ")", "g");
  return text.replace(pattern, (_match, name) => {
    const value = repl[name];
    return value === null || value === undefined ? "" : String(value);
  });
}

/** @param {string} s */
function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export { FORMS };
