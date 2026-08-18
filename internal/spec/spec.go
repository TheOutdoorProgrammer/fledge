// Package spec reads the fledge.yaml a repository keeps beside its app, so a
// release needs no arguments and CI needs no per-repository wiring.
package spec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Names are tried in order from the working directory upwards.
var Names = []string{"fledge.yaml", "fledge.yml", ".fledge.yaml", ".fledge.yml"}

// Spec is the contents of a fledge.yaml.
type Spec struct {
	Server   string `yaml:"server"`
	Audience string `yaml:"audience"`
	IPA      string `yaml:"ipa"`
	Notes    string `yaml:"notes"`
	Build    Build  `yaml:"build"`
	Strict   *bool  `yaml:"fail_on_development_signing"`

	// Path is where this was read from, empty when nothing was found.
	Path string `yaml:"-"`
}

// Build describes how to produce an archive when one is not already exported.
type Build struct {
	Project string `yaml:"project"`
	Scheme  string `yaml:"scheme"`
	Method  string `yaml:"method"`
	Team    string `yaml:"team"`
}

// FailOnDevelopmentSigning reports the setting, defaulting to off so an
// unconfigured repository is not surprised by a failing release.
func (s *Spec) FailOnDevelopmentSigning() bool {
	return s.Strict != nil && *s.Strict
}

// Find walks up from dir looking for a spec, and returns an empty one when
// there is none, because every field has a working default.
func Find(dir string) (*Spec, error) {
	start, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	for current := start; ; {
		for _, name := range Names {
			candidate := filepath.Join(current, name)
			if _, err := os.Stat(candidate); err == nil {
				return Load(candidate)
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			return &Spec{}, nil
		}
		current = parent
	}
}

// Load reads one spec file.
func Load(path string) (*Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	spec := &Spec{Path: path}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	// Unknown fields are a typo the author wants to hear about, not something
	// to silently ignore into a release that did not do what they asked.
	decoder.KnownFields(true)

	if err := decoder.Decode(spec); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	spec.Path = path

	if err := spec.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return spec, nil
}

func (s *Spec) validate() error {
	if s.Server != "" && !strings.HasPrefix(s.Server, "https://") {
		return fmt.Errorf("server must be https, got %q: iOS refuses to install over plain HTTP", s.Server)
	}
	if s.Build.Method != "" && !validMethod(s.Build.Method) {
		return fmt.Errorf("build.method %q is not an export method", s.Build.Method)
	}

	return nil
}

func validMethod(method string) bool {
	for _, known := range []string{"release-testing", "debugging", "enterprise", "app-store-connect", "developer-id", "mac-application", "validation"} {
		if method == known {
			return true
		}
	}
	return false
}

// First returns the first value that was set, callers passing flag, then
// environment, then file. That order is what lets a public repository keep its
// server URL in a secret rather than in the committed spec.
func First(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ErrNoServer says the one field with no sensible default is missing.
var ErrNoServer = errors.New("no server: set it in fledge.yaml, pass -server, or set FLEDGE_URL")
