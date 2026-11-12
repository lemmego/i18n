package i18n

import (
	"context"
	"sync/atomic"
)

type contextKey struct{}

// fallback is the translator returned when a context carries none.
//
// It exists so that a worker, a CLI command, a test or a code path nobody
// thought about gets readable English rather than raw keys. A nil translator
// returned from FromContext would push a nil check into every caller, and the
// one caller that forgot it would panic inside a mail render at three in the
// morning.
var fallback atomic.Pointer[Translator]

// SetDefault installs the translator that [FromContext] returns when a
// context carries none. The provider calls it once at boot.
func SetDefault(t *Translator) { fallback.Store(t) }

// Default returns the process-wide translator.
//
// Never nil: with nothing installed it is an empty catalogue in English,
// which returns every key unchanged. That is the right degradation, because
// the alternative is a panic in whichever path was not wired up.
func Default() *Translator {
	if t := fallback.Load(); t != nil {
		return t
	}
	return emptyTranslator()
}

// With returns a context carrying a translator.
//
// This is the primitive the rest of the framework is built on, and the reason
// it takes a context.Context rather than an app.Context: mail, notify, queue
// and tasker only ever have the former, and a per-recipient locale has to
// reach a mail body rendered on a worker goroutine with no request anywhere
// near it.
func With(ctx context.Context, t *Translator) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, t)
}

// FromContext returns the translator a context carries, or the process
// default. It never returns nil.
func FromContext(ctx context.Context) *Translator {
	if ctx != nil {
		if t, ok := ctx.Value(contextKey{}).(*Translator); ok && t != nil {
			return t
		}
	}
	return Default()
}

// LocaleOf returns the locale a context carries, or "" if it carries none.
//
// Empty rather than the default locale, because its callers need to tell
// "this request chose a language" from "nobody said": mail's renderer uses it
// to decide whether to look for a locale subdirectory of templates, and
// looking for one named after the default is a wasted stat on every send.
//
// Its shape — func(context.Context) string — is what lets mail accept it
// without importing this package.
func LocaleOf(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	t, ok := ctx.Value(contextKey{}).(*Translator)
	if !ok || t == nil {
		return ""
	}
	return t.Locale()
}

// emptyTranslator is an English catalogue with no messages in it, so every
// key renders as itself.
func emptyTranslator() *Translator {
	cat, err := NewCatalog("en")
	if err != nil {
		// "en" is a valid tag, so this cannot happen; panicking is still
		// better than returning nil into the no-nil-ever contract above.
		panic("i18n: " + err.Error())
	}
	return cat.In("en")
}
