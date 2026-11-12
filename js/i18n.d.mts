/**
 * Types for i18n.mjs.
 *
 * Hand-written rather than generated, because the implementation is plain
 * JavaScript so that `node --test` needs no toolchain — see the comment at
 * the top of i18n.mjs. A TypeScript consumer gets the same checking it would
 * from a .ts source.
 */

export type Replacements = Record<string, string | number | null | undefined>;

/** A plain message, or a set of plural forms keyed by CLDR category or "=N". */
export type Message = string | Record<string, string>;

export type Messages = Record<string, Message>;

/** The shape of the `i18n` page prop. */
export interface Payload {
  locale: string;
  fallback: string;
  dir: "ltr" | "rtl";
  messages: Messages;
}

export interface Translator {
  /** The message for a key, with its placeholders substituted. */
  t(key: string, repl?: Replacements): string;
  /** The plural form of a message for a count. `:count` is bound for you. */
  choice(key: string, n: number, repl?: Replacements): string;
  readonly locale: string;
  readonly dir: "ltr" | "rtl";
}

/**
 * createTranslator returns the reading half of a payload.
 *
 * It holds no module-level state, so one server-side renderer can produce
 * pages in several languages concurrently.
 *
 * @param onMiss called once per missing key. A development build reports
 *   them; production passes nothing.
 */
export function createTranslator(
  payload: Payload | undefined,
  onMiss?: (key: string) => void,
): Translator;

/** Substitutes `:name` placeholders, longest key first, in one pass. */
export function interpolate(text: string, repl?: Replacements): string;

/** The CLDR cardinal categories, in the order the Go side lists them. */
export const FORMS: readonly string[];
