package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// readGroup turns one JSON file into flat dotted keys.
//
// resources/lang/en/validation.json holding {"min": {"string": "..."}} yields
// the key validation.min.string, so the key space is the file layout and
// there is nothing to keep in sync.
func readGroup(path, prefix string, data []byte, out func(key string, m message)) error {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		// A syntax error carries a byte offset, which is the difference
		// between a boot failure somebody can fix and one they have to bisect.
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			line, col := lineAndColumn(data, syntax.Offset)
			return errAt(path, "invalid JSON at line %d, column %d: %s", line, col, syntax.Error())
		}
		return errAt(path, "invalid JSON: %s", err)
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return errAt(path, "the file must hold a JSON object of messages, not %s", jsonKind(root))
	}
	return flatten(path, prefix, obj, out)
}

func flatten(path, prefix string, obj map[string]any, out func(key string, m message)) error {
	for name, value := range obj {
		if name == "" {
			return errAt(path, "the key %q has an empty segment", prefix)
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}

		switch v := value.(type) {
		case string:
			out(key, message{text: v})

		case map[string]any:
			forms, isPlural, err := pluralSet(path, key, v)
			if err != nil {
				return err
			}
			if isPlural {
				out(key, message{forms: forms})
				continue
			}
			if len(v) == 0 {
				return errAt(path, "%s is an empty object; a message is a string, "+
					"and a group of messages has at least one", key)
			}
			if err := flatten(path, key, v, out); err != nil {
				return err
			}

		default:
			return errAt(path, "%s is %s; a message must be a string, or an object "+
				"of plural forms", key, jsonKind(value))
		}
	}
	return nil
}

// pluralSet decides whether an object is a set of plural forms or a nesting
// level, and refuses to guess.
//
// A JSON object is ambiguous between the two, and the ambiguity is resolved by
// rule rather than by heuristic: every key is a form name or an exact count,
// or none of them is. An object that mixes the two is a load error naming the
// offending key, because the alternative is that one message quietly
// disappears in one language — the plural branch swallows the nested group,
// or the nested branch makes "one" a message key nobody reads.
func pluralSet(path, key string, obj map[string]any) (map[string]string, bool, error) {
	var formal, informal []string
	for name := range obj {
		if validForm(name) {
			formal = append(formal, name)
			continue
		}
		informal = append(informal, name)
	}
	if len(formal) == 0 {
		return nil, false, nil
	}
	if len(informal) > 0 {
		slices.Sort(formal)
		slices.Sort(informal)
		return nil, false, errAt(path, "%s mixes plural forms (%s) with ordinary keys (%s), "+
			"so it is neither a plural message nor a group of messages. Plural forms are %s "+
			"and exact counts like \"=0\"", key, strings.Join(formal, ", "),
			strings.Join(informal, ", "), strings.Join(Forms, ", "))
	}

	forms := make(map[string]string, len(obj))
	for name, value := range obj {
		text, ok := value.(string)
		if !ok {
			return nil, false, errAt(path, "the plural form %s.%s is %s; it must be a string",
				key, name, jsonKind(value))
		}
		forms[name] = text
	}
	if _, ok := forms["other"]; !ok {
		// Required so selection always has a floor, which is what makes a
		// form a translator did not write degrade to the plural rather than
		// to the singular. Every CLDR locale uses "other", including the ones
		// that use nothing else.
		return nil, false, errAt(path, "the plural message %s has no \"other\" form. "+
			"Every language uses it, and it is what a form this language does not list "+
			"falls back to", key)
	}
	return forms, true, nil
}

func jsonKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func lineAndColumn(data []byte, offset int64) (line, col int) {
	line, col = 1, 1
	for i := int64(0); i < offset && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}
