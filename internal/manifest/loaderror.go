package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// friendlyLoadError translates the low-level encoding/json errors produced when
// decoding fglpkg.json into field-named, example-carrying messages, so an
// ill-formed manifest names the offending property in manifest terms rather
// than leaking Go-internal detail like
// "json: cannot unmarshal string into Go struct field Manifest.docs of type []string".
//
// It handles type mismatches (*json.UnmarshalTypeError) and the
// DisallowUnknownFields "unknown field" error. Everything else — syntax errors
// and the already-friendly messages from the custom UnmarshalJSON methods on
// Dependencies/Hooks/HookOperation — is passed through with the same
// "invalid fglpkg.json: %w" wrapping Load has always used.
//
// The per-field logic is deliberately shared so the forthcoming `fglpkg lint`
// command (GIS-270) can reuse it.
func friendlyLoadError(err error) error {
	if err == nil {
		return nil
	}

	// Legacy "scripts" field: keep the migration hint. This must run before the
	// generic unknown-field branch, since the underlying error is itself a
	// DisallowUnknownFields "unknown field" error.
	if strings.Contains(err.Error(), `"scripts"`) {
		return fmt.Errorf(
			`invalid %s: the "scripts" field has been replaced by "hooks" with declarative operations — see docs/user-guide.md`,
			Filename,
		)
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf("invalid %s: %s", Filename, describeTypeError(typeErr))
	}

	if field, ok := unknownField(err); ok {
		// Case-insensitive: DisallowUnknownFields rejects "Signing"/"signing"
		// alike (no such struct field in any case), so the hint should fire for
		// either spelling rather than falling through to the generic typo message.
		if hint, isPolicy := policyOnlyKeys[strings.ToLower(field)]; isPolicy {
			return fmt.Errorf(
				`invalid %s: %q is a user/global setting, not a project field — %s`,
				Filename, field, hint,
			)
		}
		return fmt.Errorf(
			`invalid %s: unknown field %q — check for a typo; see the allowed fields in schema/fglpkg.schema.json`,
			Filename, field,
		)
	}

	return fmt.Errorf("invalid %s: %w", Filename, err)
}

// policyOnlyKeys are config keys that are deliberately NOT manifest fields:
// signature enforcement, update-check preferences, and credentials are user/
// global concerns and, per GIS-368, must not be settable by a checked-in repo
// config (a repo could otherwise weaken a user's signature checks). Naming one in
// fglpkg.json is rejected with a pointer to where it belongs rather than a bare
// "unknown field".
// Keyed by the lower-cased field name (see the strings.ToLower lookup above).
var policyOnlyKeys = map[string]string{
	"signing":             `set signature enforcement in ~/.fglpkg/config.json ("signing": {"enforce": "require|warn|off"}) or via FGLPKG_SIGNING`,
	"updatecheck":         "the update-check preference is a user setting in ~/.fglpkg/config.json, not a per-project one",
	"updatecheckinterval": "the update-check interval is a user setting in ~/.fglpkg/config.json, not a per-project one",
	"credentials":         "credentials live in ~/.fglpkg/credentials.json (via 'fglpkg login'), never in a checked-in file",
	"token":               "authentication tokens are never stored in fglpkg.json; use 'fglpkg login' or the FGLPKG_TOKEN env var",
}

// fieldHints maps a user-facing manifest key to a phrase describing the value
// it expects, with a short example. Keys not listed fall back to a description
// derived from the Go type in the underlying error.
var fieldHints = map[string]string{
	"docs":          `an array of glob patterns (e.g. ["README.md"])`,
	"files":         `an array of glob patterns (e.g. ["*.42m","*.42f"])`,
	"programs":      `an array of module names (e.g. ["PoiConvert"])`,
	"keywords":      `an array of strings (e.g. ["database","utilities"])`,
	"include":       `an array of file paths (e.g. ["LICENSE"])`,
	"profile":       `an array of file paths (e.g. ["profiles/app.4gp"])`,
	"webcomponents": `an array of COMPONENTTYPE names (e.g. ["MyChart"])`,
	"bin":           `an object mapping command names to script paths`,
}

// describeTypeError renders a *json.UnmarshalTypeError as
// `<key>: expected <hint>, got <actual>`.
func describeTypeError(e *json.UnmarshalTypeError) string {
	// e.Field is the JSON-tag path (e.g. "docs"); the leaf key is what the user
	// wrote in the manifest. It may be empty when the mismatch happened inside a
	// nested decode that lost struct context.
	key := e.Field
	if i := strings.LastIndex(key, "."); i >= 0 {
		key = key[i+1:]
	}

	hint, ok := fieldHints[key]
	if !ok {
		hint = describeGoType(e.Type)
	}

	got := describeJSONValue(e.Value)

	if key == "" {
		return fmt.Sprintf("expected %s, got %s", hint, got)
	}
	return fmt.Sprintf("%s: expected %s, got %s", key, hint, got)
}

// describeJSONValue turns encoding/json's actual-value word (e.g. "string",
// "number", "bool", "array", "object", "null") into a readable "a/an <word>".
func describeJSONValue(v string) string {
	switch v {
	case "bool":
		return "a boolean"
	case "array":
		return "an array"
	case "object":
		return "an object"
	case "number":
		return "a number"
	case "string":
		return "a string"
	case "null":
		return "null"
	case "":
		return "a value of the wrong type"
	default:
		return withArticle(v)
	}
}

// describeGoType is the fallback expected-type phrase, derived from the Go type
// the decoder was targeting.
func describeGoType(t reflect.Type) string {
	if t == nil {
		return "a different type"
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.String {
			return "an array of strings"
		}
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	default:
		return withArticle(t.String())
	}
}

// withArticle prefixes a word with "a"/"an" based on its first letter.
func withArticle(word string) string {
	if word == "" {
		return word
	}
	switch word[0] {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return "an " + word
	default:
		return "a " + word
	}
}

// unknownField extracts the field name from a DisallowUnknownFields error of the
// form `json: unknown field "X"`, returning ("X", true) on a match.
func unknownField(err error) (string, bool) {
	msg := err.Error()
	const marker = `unknown field "`
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}
