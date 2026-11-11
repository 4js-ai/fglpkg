package installer

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// pkgEncoding is what the in-zip manifest says about the package's build
// encoding. Both fields are optional, and both being empty is the normal case
// for every package published before GIS-285 landed.
type pkgEncoding struct {
	CharacterEncoding string `json:"characterEncoding"`
	LengthSemantics   string `json:"lengthSemantics"`
	// StringResourceEncodings names the charset of each .42s carrying
	// non-ASCII text. It WARNS and never refuses: a package may ship one
	// resource file per language and charset for the consumer to select at
	// runtime, so a file that does not match this environment may simply be
	// one the consumer never loads.
	StringResourceEncodings map[string]string `json:"stringResourceEncodings"`
}

// readEncodingFromZip peeks at the in-zip manifest for the encoding fields,
// without extracting anything. It mirrors readWebcomponentsFromZip, and like
// it uses a partial decode so an unknown or newer manifest field cannot make
// the read fail.
func readEncodingFromZip(zipPath string) (pkgEncoding, error) {
	var enc pkgEncoding
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return enc, err
	}
	defer r.Close()
	for _, f := range r.File {
		if filepath.ToSlash(f.Name) != manifest.Filename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return enc, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return enc, err
		}
		if err := json.Unmarshal(data, &enc); err != nil {
			return enc, fmt.Errorf("manifest in zip is not valid JSON: %w", err)
		}
		return enc, nil
	}
	return enc, nil
}

// checkEncoding refuses a package whose build charset or length semantics the
// local Genero environment cannot run correctly (GIS-285).
//
// It is deliberately quiet in every case but a VERIFIED mismatch:
//
//   - absent declaration  → proceed silently. Every package on the registry
//     predates the field, so warning here would make `install` noisy for
//     everyone while telling them nothing they can act on. The field
//     populates itself as packages are republished, because pack derives it.
//   - US-ASCII            → proceed, no check. ASCII-7 is a subset of every
//     charset Genero supports, so such a package is portable by construction.
//   - equal to the local charmap → proceed.
//   - anything else       → refuse, naming both charsets and the escape hatch.
//
// String resource encodings are reported separately, as warnings that never
// refuse. See warnResourceEncodings.
func (i *Installer) checkEncoding(zipPath, pkgName string) error {
	enc, err := readEncodingFromZip(zipPath)
	if err != nil {
		return fmt.Errorf("cannot read manifest from zip: %w", err)
	}
	pkgCharset := strings.TrimSpace(enc.CharacterEncoding)
	pkgLength := strings.ToUpper(strings.TrimSpace(enc.LengthSemantics))

	// Nothing declared: no check is possible, and none is wanted.
	if pkgCharset == "" && pkgLength == "" && len(enc.StringResourceEncodings) == 0 {
		return nil
	}
	// A pure-ASCII package with no resource constraint needs no environment at
	// all, so avoid running fglrun when that is the only question.
	if pkgLength == "" && len(enc.StringResourceEncodings) == 0 &&
		charmap.Equal(pkgCharset, charmap.ASCII) {
		return nil
	}

	info, derr := genero.DetectRuntimeInfo()
	// An empty Charmap is the same situation as an error: the environment
	// could not be read. Without this, a missing fglrun plus a set
	// FGLPKG_LENGTH_SEMANTICS made DetectRuntimeInfo return no error and an
	// empty charmap, and every declared charset then "mismatched" it —
	// refusing with "but this environment uses " and nothing after it.
	if derr != nil || strings.TrimSpace(info.Charmap) == "" {
		// The package states a requirement and we cannot read the environment
		// to check it. Warn rather than refuse: failing here would make
		// fglpkg unusable wherever fglrun is absent (a build container that
		// only vendors packages), and the declaration is advisory until a
		// program actually runs.
		reason := "it reported no charmap"
		if derr != nil {
			reason = derr.Error()
		}
		fmt.Fprintf(os.Stderr,
			"warning: %s declares a character encoding requirement but the local Genero "+
				"charmap could not be read, so it was not checked: %s\n",
			pkgName, reason)
		return nil
	}
	warnResourceEncodings(pkgName, enc.StringResourceEncodings, info.Charmap)

	var problems []string
	if pkgCharset != "" &&
		!charmap.Equal(pkgCharset, charmap.ASCII) &&
		!charmap.Equal(pkgCharset, info.Charmap) {
		problems = append(problems, fmt.Sprintf(
			"built with character encoding %s, but this environment uses %s",
			charmap.Canonical(pkgCharset), charmap.Canonical(info.Charmap)))
	}
	if pkgLength != "" && info.LengthSemantics != "" && pkgLength != info.LengthSemantics {
		problems = append(problems, fmt.Sprintf(
			"requires FGL_LENGTH_SEMANTICS=%s, but this environment reports %s",
			pkgLength, info.LengthSemantics))
	}
	if len(problems) == 0 {
		return nil
	}

	if i.forceCharacterEncoding {
		fmt.Fprintf(os.Stderr,
			"warning: installing %s despite an encoding mismatch (--force-character-encoding):\n  - %s\n"+
				"  String literals in this package may display incorrectly and string lengths may be wrong.\n",
			pkgName, strings.Join(problems, "\n  - "))
		return nil
	}
	// Name the command as well as the flag. This refusal also fires during
	// `fglpkg update`, which REJECTS --force-character-encoding ("not valid
	// for 'update'"), so advising the bare flag sent the user to a second
	// error. `fglpkg install --force-character-encoding` is correct from
	// either command.
	return fmt.Errorf(
		"%s is not compatible with this Genero environment:\n  - %s\n"+
			"  Install it under a matching environment, or run "+
			"'fglpkg install --force-character-encoding' to install anyway (at your own risk).",
		pkgName, strings.Join(problems, "\n  - "))
}

// warnResourceEncodings reports each string resource whose charset this
// environment cannot read correctly. It never refuses.
//
// A .42s is used as its bytes stand, with no transcoding — verified with the
// real runtime: a UTF-8 resource under an ISO-8859-15 runtime shows raw bytes
// and reports length 12 for a 10-character string, and a Latin-9 resource
// under a UTF-8 runtime is cut off at the first invalid byte. So a mismatch is
// real. But a package may ship one resource file per language and charset for
// the consumer to select at runtime, and refusing the whole package because
// one of them does not match would block a consumer who was going to select
// another.
func warnResourceEncodings(pkgName string, resources map[string]string, charmapName string) {
	var mismatched []string
	for path, enc := range resources {
		if strings.TrimSpace(enc) == "" ||
			charmap.Equal(enc, charmap.ASCII) ||
			charmap.Equal(enc, charmapName) {
			continue
		}
		mismatched = append(mismatched, fmt.Sprintf("%s is %s", path, charmap.Canonical(enc)))
	}
	if len(mismatched) == 0 {
		return
	}
	sort.Strings(mismatched)
	fmt.Fprintf(os.Stderr,
		"warning: %s ships string resources this environment (%s) cannot read correctly:\n  - %s\n"+
			"  Text from those files will display incorrectly and report the wrong length. "+
			"Select a resource file matching your charmap, or rebuild the .str under it.\n",
		pkgName, charmap.Canonical(charmapName), strings.Join(mismatched, "\n  - "))
}
