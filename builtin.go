package i18n

import (
	"embed"
	"io/fs"
	"maps"
	"sync"
)

//go:embed catalog
var builtinFS embed.FS

// Builtin returns the framework's own English messages as a filesystem, laid
// out the way an application's catalogue is: catalog/en/validation.json and so
// on.
//
// A module's provider hands these to the publishing machinery so that
// `lemmego run publish --tags=i18n` writes them into resources/lang for
// editing — which is what Laravel's lang:publish does, and needs no new
// command here because api/app/publish.go already refuses to overwrite a file
// somebody has edited.
func Builtin() fs.FS {
	sub, err := fs.Sub(builtinFS, "catalog")
	if err != nil {
		// The directory is embedded at compile time, so this cannot happen.
		panic("i18n: " + err.Error())
	}
	return sub
}

// builtinMessages parses the embedded English once and shares it.
//
// Shared because it is immutable and identical for every catalogue in the
// process, and parsing a few hundred messages per test would be waste. A
// failure here is a defect in this module rather than in an application, so
// it panics: the files are embedded, so a malformed one could never have
// passed this module's own tests.
var builtinMessages = sync.OnceValue(func() map[string]message {
	cat := &Catalog{}
	collect := map[string]map[string]message{}
	if err := cat.readLocale(Builtin(), "en", "en", "", collect); err != nil {
		panic("i18n: the embedded English catalogue is broken: " + err.Error())
	}
	return maps.Clone(collect["en"])
})

// Keys returns every key the framework's own English defines, sorted. It is
// what a parity test in the api module compares its floor against.
func BuiltinKeys() []string {
	cat := &Catalog{mounted: map[string]map[string]message{"en": builtinMessages()}}
	return cat.Keys("en")
}

// BuiltinMessage returns the framework's own English for a key.
func BuiltinMessage(key string) (text string, forms map[string]string, ok bool) {
	m, ok := builtinMessages()[key]
	if !ok {
		return "", nil, false
	}
	return m.text, m.forms, true
}
