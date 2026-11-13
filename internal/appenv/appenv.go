// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package appenv reads apps/<app>.env, the four inputs that differ between
// one app's FrankenPHP binary and another's, and refuses a file that says
// anything else.
//
// Every build takes its inputs from here: `just build-app` and the build
// workflow both pass the build arguments cmd/frankenphp-buildargs prints,
// and the app's name as APP themselves. A file cannot override a pin the
// Containerfile holds, such as PHP_VERSION or SPC_COMMIT, where Renovate
// would never see it and the in-build checks would verify the override. A
// misspelt or dropped input fails before the build starts, where BuildKit
// would only warn that an argument went unused and a check would quietly
// be skipped.
//
// The file holds lines of KEY=value:
//
//   - exactly the keys PHP_EXTENSIONS, PHP_EXTENSION_LIBS,
//     EXPECTED_EXTENSIONS and REQUIRED_GD_FEATURES, each once, and never
//     APP, which the build passes;
//   - the key is everything before the first "=", and the value everything
//     after it, verbatim and possibly empty;
//   - EXPECTED_EXTENSIONS not empty, the others possibly;
//   - no `"` anywhere, and no whitespace at either end of a key or a value;
//   - lines that are blank, or start with "#", are skipped;
//   - the last line is read whether or not a newline ends it.
//
// Every line that breaks a rule, and every key left out, is reported, each
// naming the file, the line and the key.
package appenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Inputs are one app's build arguments, as its file sets them.
type Inputs struct {
	PHPExtensions      string // PHP_EXTENSIONS
	PHPExtensionLibs   string // PHP_EXTENSION_LIBS
	ExpectedExtensions string // EXPECTED_EXTENSIONS
	RequiredGDFeatures string // REQUIRED_GD_FEATURES
}

// What a file can get wrong. Each error Parse and Load return wraps one.
var (
	// ErrMalformed is a line that is not KEY=value.
	ErrMalformed = errors.New("not a KEY=value line")
	// ErrUnknown is a key that is not one of the four inputs, such as a
	// pin the Containerfile holds or a misspelling.
	ErrUnknown = errors.New("not one of PHP_EXTENSIONS, PHP_EXTENSION_LIBS, EXPECTED_EXTENSIONS and REQUIRED_GD_FEATURES")
	// ErrDuplicate is a key set more than once.
	ErrDuplicate = errors.New("set more than once")
	// ErrMissing is a key never set.
	ErrMissing = errors.New("never set")
	// ErrEmpty is EXPECTED_EXTENSIONS set to nothing. The build declares it
	// only after the binary's long build, where extensions.sh would refuse
	// it, so this is the only refusal before the build starts.
	ErrEmpty = errors.New("is empty; it must name every module the binary loads")
	// ErrQuote is a line holding a `"`, which would reach the build as part
	// of the value.
	ErrQuote = errors.New(`holds a " character, which the build would take as part of the value`)
	// ErrPadding is whitespace at either end of a key or a value.
	ErrPadding = errors.New("has whitespace at one end of its key or value")
	// ErrApp is APP set in the file. The build passes it, from the file's
	// name.
	ErrApp = errors.New("is the build's to pass, never the file's")
	// ErrAppName is an app name that is not lower-case words joined by
	// hyphens, and so names no file in the apps directory.
	ErrAppName = errors.New("not an app name: lower-case letters and digits, joined by hyphens")
)

var appName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Load reads and parses <app>.env from root, the apps directory.
func Load(root *os.Root, app string) (Inputs, error) {
	if !appName.MatchString(app) {
		return Inputs{}, fmt.Errorf("%q: %w", app, ErrAppName)
	}
	file := app + ".env"
	data, err := root.ReadFile(file)
	if err != nil {
		return Inputs{}, fmt.Errorf("read %s: %w", filepath.Join(root.Name(), file), err)
	}
	return Parse(filepath.Join(root.Name(), file), data)
}

// Parse parses data, an app's file, which errors call name. It returns
// every problem in the file joined into one error, and the zero Inputs
// with it.
func Parse(name string, data []byte) (Inputs, error) {
	var in Inputs
	fields := fields(&in)
	setOn := map[string]int{}
	var errs []error
	number := 0
	for line := range strings.Lines(string(data)) {
		number++
		line = strings.TrimSuffix(line, "\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rawKey, rawValue, ok := strings.Cut(line, "=")
		key, value := strings.TrimSpace(rawKey), strings.TrimSpace(rawValue)
		if !ok || key == "" {
			errs = append(errs, fmt.Errorf("%s:%d: %q: %w", name, number, line, ErrMalformed))
			continue
		}
		at := fmt.Sprintf("%s:%d: %s", name, number, key)
		if strings.Contains(line, `"`) {
			errs = append(errs, fmt.Errorf("%s: %w", at, ErrQuote))
		}
		if key != rawKey || value != rawValue {
			errs = append(errs, fmt.Errorf("%s: %w", at, ErrPadding))
		}
		field, known := fields[key]
		switch {
		case key == "APP":
			errs = append(errs, fmt.Errorf("%s: %w", at, ErrApp))
		case !known:
			errs = append(errs, fmt.Errorf("%s: %w", at, ErrUnknown))
		case setOn[key] != 0:
			errs = append(errs, fmt.Errorf("%s: %w, first on line %d", at, ErrDuplicate, setOn[key]))
		default:
			setOn[key] = number
			*field = value
			if key == "EXPECTED_EXTENSIONS" && value == "" {
				errs = append(errs, fmt.Errorf("%s: %w", at, ErrEmpty))
			}
		}
	}
	for _, key := range keys {
		if setOn[key] == 0 {
			errs = append(errs, fmt.Errorf("%s: %s: %w", name, key, ErrMissing))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Inputs{}, err
	}
	return in, nil
}

// Args returns the build arguments, one KEY=value each, in a fixed order.
func (in Inputs) Args() []string {
	fields := fields(&in)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, key+"="+*fields[key])
	}
	return args
}

// keys are the four inputs, in the order Args returns them.
var keys = []string{"PHP_EXTENSIONS", "PHP_EXTENSION_LIBS", "EXPECTED_EXTENSIONS", "REQUIRED_GD_FEATURES"}

// fields maps each key to the field of in that holds it.
func fields(in *Inputs) map[string]*string {
	return map[string]*string{
		"PHP_EXTENSIONS":       &in.PHPExtensions,
		"PHP_EXTENSION_LIBS":   &in.PHPExtensionLibs,
		"EXPECTED_EXTENSIONS":  &in.ExpectedExtensions,
		"REQUIRED_GD_FEATURES": &in.RequiredGDFeatures,
	}
}
