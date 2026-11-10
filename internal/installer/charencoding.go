package installer

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
func (i *Installer) checkEncoding(zipPath, pkgName string) error {
	enc, err := readEncodingFromZip(zipPath)
	if err != nil {
		return fmt.Errorf("cannot read manifest from zip: %w", err)
	}
	pkgCharset := strings.TrimSpace(enc.CharacterEncoding)
	pkgLength := strings.ToUpper(strings.TrimSpace(enc.LengthSemantics))

	// Nothing declared: no check is possible, and none is wanted.
	if pkgCharset == "" && pkgLength == "" {
		return nil
	}
	// A pure-ASCII package needs no environment at all to clear the charset
	// half, so avoid running fglrun when that is the only question.
	if pkgLength == "" && charmap.Equal(pkgCharset, charmap.ASCII) {
		return nil
	}

	info, derr := genero.DetectRuntimeInfo()
	if derr != nil {
		// The package states a requirement and we cannot read the environment
		// to check it. Warn rather than refuse: failing here would make
		// fglpkg unusable wherever fglrun is absent (a build container that
		// only vendors packages), and the declaration is advisory until a
		// program actually runs.
		fmt.Fprintf(os.Stderr,
			"warning: %s declares a character encoding requirement but the local Genero "+
				"charmap could not be read, so it was not checked: %v\n",
			pkgName, derr)
		return nil
	}

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
	return fmt.Errorf(
		"%s is not compatible with this Genero environment:\n  - %s\n"+
			"  Install it under a matching environment, or pass --force-character-encoding "+
			"to install anyway (at your own risk).",
		pkgName, strings.Join(problems, "\n  - "))
}
