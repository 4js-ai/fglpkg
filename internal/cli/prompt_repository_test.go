package cli

import (
	"strings"
	"testing"
)

// promptNonEmptyString reuses its label verbatim in the "Invalid %s - cannot
// be empty" error, lower-cased. Folding the guidance into the label therefore
// leaked it into the error:
//
//	error: Invalid repository (source-code url, e.g. https://github.com/owner/repo) - cannot be empty
//
// with the URL lower-cased too. The guidance now prints on its own line, so
// the error text is back to naming just the field.
func TestPromptRepositoryErrorTextNamesOnlyTheField(t *testing.T) {
	// Empty line, then a value: the first Enter triggers the error path.
	withStdin(t, "\nhttps://github.com/example/demo\n")

	out, err := captureStdout(t, func() error {
		v, e := promptRepository("")
		if e != nil {
			return e
		}
		if v != "https://github.com/example/demo" {
			t.Errorf("value = %q, want the typed URL", v)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("promptRepository: %v", err)
	}

	const want = "error: Invalid repository - cannot be empty"
	if !strings.Contains(out, want) {
		t.Errorf("output does not contain %q; got:\n%s", want, out)
	}
	// The guidance must not have been dragged into the error line.
	for _, leak := range []string{
		"Invalid repository (",
		"source-code url", // the lower-cased leak
		"e.g. https://github.com/owner/repo) - cannot be empty",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("the prompt hint leaked into the error text: %q found in:\n%s", leak, out)
		}
	}
	// The hint itself must still be shown — it is why the label was changed.
	if !strings.Contains(out, "https://github.com/owner/repo") {
		t.Errorf("the guidance was lost entirely; got:\n%s", out)
	}
}

// With a detected git remote the value is offered as the default. The label
// must stay bare, or promptWithDefault's own parentheses double up:
//
//	Repository (source-code URL) (https://github.com/example/demo):
func TestPromptRepositoryWithDetectedRemoteHasNoDoubledParentheses(t *testing.T) {
	withStdin(t, "\n") // bare Enter accepts the detected default

	out, err := captureStdout(t, func() error {
		v, e := promptRepository("https://github.com/example/demo")
		if e != nil {
			return e
		}
		if v != "https://github.com/example/demo" {
			t.Errorf("value = %q, want the detected default", v)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("promptRepository: %v", err)
	}
	if strings.Contains(out, ") (") {
		t.Errorf("doubled parentheses in the prompt: %q", out)
	}
	if !strings.Contains(out, "Repository (https://github.com/example/demo)") {
		t.Errorf("prompt = %q, want the bare label with the detected default", out)
	}
}

// The other two init prompts share the helper and must be unaffected.
func TestPromptNonEmptyStringErrorTextUnchanged(t *testing.T) {
	withStdin(t, "\nA description\n")
	out, err := captureStdout(t, func() error {
		_, e := promptNonEmptyString("Description")
		return e
	})
	if err != nil {
		t.Fatalf("promptNonEmptyString: %v", err)
	}
	if !strings.Contains(out, "error: Invalid description - cannot be empty") {
		t.Errorf("output does not carry the unchanged error text; got:\n%s", out)
	}
}
