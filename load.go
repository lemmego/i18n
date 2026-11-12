package i18n

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"golang.org/x/text/language"
)

// vendorDir is where an application's overrides of a module's messages live,
// mirroring where Laravel puts the same thing.
const vendorDir = "vendor"

// LoadFS builds a catalogue from a filesystem.
//
// The layout is one directory per locale and one JSON file per group, so
// <root>/en/validation.json yields the keys validation.*:
//
//	resources/lang/
//	  en/validation.json          -> validation.required
//	  en/auth.json                -> auth.failed
//	  fr/validation.json
//	  vendor/authz/fr/errors.json -> authz::errors.forbidden
//
// An fs.FS rather than a directory is the real input, so a single-binary
// deployment embeds its catalogue with //go:embed and a test builds one from
// fstest.MapFS. [LoadDir] is the two-line convenience over it.
//
// Loading is eager and covers every locale, and a malformed file fails here
// rather than on the first request. A thousand-message catalogue is tens of
// kilobytes and parses in single-digit milliseconds, and in exchange a broken
// file is found by whoever broke it instead of by the first reader of a
// language nobody on the team reads. That failure is invisible by nature,
// which is the whole argument for paying for it at boot.
func LoadFS(fsys fs.FS, root string, supported ...string) (*Catalog, error) {
	cat, err := NewCatalog(supported...)
	if err != nil {
		return nil, err
	}
	if err := cat.LoadFS(fsys, root); err != nil {
		return nil, err
	}
	return cat, nil
}

// LoadDir builds a catalogue from a directory on disk.
//
// A missing directory is not an error: an application that has published no
// catalogue yet still boots, and every key falls through to whatever its
// modules mounted. A directory that exists and is broken is an error.
func LoadDir(dir string, supported ...string) (*Catalog, error) {
	cat, err := NewCatalog(supported...)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return cat, nil
	}
	if err := cat.LoadFS(os.DirFS(dir), "."); err != nil {
		return nil, err
	}
	return cat, nil
}

// LoadFS reads an application's own messages into the catalogue, including
// any overrides under vendor/.
func (c *Catalog) LoadFS(fsys fs.FS, root string) error {
	locales, err := localeDirs(fsys, root, c.neg.locales)
	if err != nil {
		return err
	}
	for _, locale := range locales {
		if err := c.readLocale(fsys, path.Join(root, locale), locale, "", c.app); err != nil {
			return err
		}
	}
	return c.loadOverrides(fsys, path.Join(root, vendorDir))
}

// loadOverrides reads <root>/vendor/<namespace>/<locale>/<group>.json.
//
// These land in the same map as the application's own messages, one level
// above whatever the module mounted, which is what makes load order
// irrelevant: an override read before or after the module registers its
// defaults wins either way.
func (c *Catalog) loadOverrides(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil // no vendor/ at all is the normal case
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ns := entry.Name()
		nsDir := path.Join(dir, ns)
		locales, err := localeDirs(fsys, nsDir, c.neg.locales)
		if err != nil {
			return err
		}
		for _, locale := range locales {
			if err := c.readLocale(fsys, path.Join(nsDir, locale), locale, ns+"::", c.app); err != nil {
				return err
			}
		}
	}
	return nil
}

// Mount registers the messages a module ships, under a namespace.
//
// A module embeds its own catalogue and mounts it, so its keys are reached as
// "authz::errors.forbidden":
//
//	//go:embed lang
//	var catalogFS embed.FS
//
//	cat.Mount("authz", catalogFS, "lang")
//
// An application overrides any of them by publishing the file to
// resources/lang/vendor/authz/<locale>/errors.json and editing it — which
// needs no new command, because a module's provider already returns those
// files from AddPublishables and `publish --tags=i18n` lays them out.
// Overriding one message keeps the rest, because the two are separate maps
// consulted in order rather than one map merged on load.
func (c *Catalog) Mount(ns string, fsys fs.FS, root string) error {
	if ns == "" || strings.Contains(ns, "::") || strings.Contains(ns, ".") {
		return fmt.Errorf("i18n: %q is not a usable namespace; it becomes the part of a key "+
			"before \"::\", so it may not contain \"::\" or \".\"", ns)
	}
	locales, err := localeDirs(fsys, root, c.neg.locales)
	if err != nil {
		return err
	}
	for _, locale := range locales {
		if err := c.readLocale(fsys, path.Join(root, locale), locale, ns+"::", c.mounted); err != nil {
			return err
		}
	}
	return nil
}

// readLocale reads every .json file in one locale directory.
func (c *Catalog) readLocale(fsys fs.FS, dir, locale, prefix string, into map[string]map[string]message) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		file := path.Join(dir, name)
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return errAt(file, "unreadable: %s", err)
		}
		group := prefix + strings.TrimSuffix(name, ".json")
		if err := readGroup(file, group, data, func(key string, m message) {
			c.put(into, locale, key, m)
		}); err != nil {
			return err
		}
	}
	return nil
}

// localeDirs lists the locale directories under a root, checking each name.
//
// Two checks, and both exist because of failures that are invisible on the
// machine where the directory was created.
//
// A name that is not its own canonical spelling is an error naming the
// canonical form. "pt-br" parses to the tag pt-BR, so the directory would be
// found on a case-insensitive filesystem and missed on Linux: the locale
// would work on every laptop and silently serve the default in the image.
//
// A canonical name that is not in the supported list is also an error, rather
// than being skipped. A skipped directory is a translated locale that does
// nothing, with no message anywhere saying why — and the fix is one line in
// either direction, so there is no reason to guess which the author meant.
func localeDirs(fsys fs.FS, root string, supported []string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, nil
	}

	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == vendorDir {
			continue
		}
		tag, err := language.Parse(name)
		if err != nil {
			return nil, errAt(path.Join(root, name), "the directory name is not a language tag: %s", err)
		}
		if canonical := tag.String(); canonical != name {
			return nil, errAt(path.Join(root, name), "the directory should be named %q; "+
				"a case-insensitive filesystem would find it either way and Linux would not, "+
				"so this works until it is deployed", canonical)
		}
		if !slices.Contains(supported, name) {
			return nil, errAt(path.Join(root, name), "%q is not a supported locale, so nothing "+
				"would read these messages. Either add it to the supported list or remove the "+
				"directory; this application supports %s", name, strings.Join(supported, ", "))
		}
		found = append(found, name)
	}
	slices.Sort(found)
	return found, nil
}
