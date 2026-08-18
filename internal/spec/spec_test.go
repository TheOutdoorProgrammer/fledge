package spec

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

func TestLoadReadsEveryField(t *testing.T) {
	path := write(t, t.TempDir(), "fledge.yaml", `
server: https://fledge.example
audience: https://fledge.example
ipa: build/*.ipa
notes: nightly
fail_on_development_signing: true
build:
  project: MyApp.xcodeproj
  scheme: MyApp
  method: release-testing
  team: ABCDE12345
`)

	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if config.Server != "https://fledge.example" || config.IPA != "build/*.ipa" || config.Notes != "nightly" {
		t.Errorf("top level = %+v", config)
	}
	if !config.FailOnDevelopmentSigning() {
		t.Error("fail_on_development_signing did not survive the round trip")
	}
	if config.Build.Scheme != "MyApp" || config.Build.Team != "ABCDE12345" {
		t.Errorf("build = %+v", config.Build)
	}
}

// TestLoadRejectsPlainHTTP catches at configuration time what iOS would
// otherwise refuse silently on the device.
func TestLoadRejectsPlainHTTP(t *testing.T) {
	path := write(t, t.TempDir(), "fledge.yaml", "server: http://fledge.example\n")

	if _, err := Load(path); err == nil {
		t.Fatal("a plain HTTP server was accepted")
	}
}

func TestLoadRejectsAnUnknownExportMethod(t *testing.T) {
	path := write(t, t.TempDir(), "fledge.yaml", "build:\n  method: ad-hoc-ish\n")

	if _, err := Load(path); err == nil {
		t.Fatal("an invented export method was accepted")
	}
}

// TestLoadRejectsATypo is why KnownFields is on: a misspelled key that is
// silently ignored produces a release that quietly did not do what was asked.
func TestLoadRejectsATypo(t *testing.T) {
	path := write(t, t.TempDir(), "fledge.yaml", "sever: https://fledge.example\n")

	if _, err := Load(path); err == nil {
		t.Fatal("a misspelled key was ignored instead of reported")
	}
}

func TestFindWalksUpward(t *testing.T) {
	root := t.TempDir()
	write(t, root, "fledge.yaml", "server: https://fledge.example\n")
	nested := filepath.Join(root, "ios", "App")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	config, err := Find(nested)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if config.Server != "https://fledge.example" {
		t.Errorf("Find from a subdirectory did not reach the root spec: %+v", config)
	}
}

func TestFindIsHappyWithNoSpec(t *testing.T) {
	config, err := Find(t.TempDir())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if config.Path != "" || config.Server != "" {
		t.Errorf("expected an empty spec, got %+v", config)
	}
}

// TestFirstPrefersTheMostSpecific is the precedence that lets a public
// repository keep its server URL in a secret rather than in a committed file.
func TestFirstPrefersTheMostSpecific(t *testing.T) {
	const (
		flag        = "https://flag.example"
		environment = "https://env.example"
		file        = "https://file.example"
	)

	if got := First(flag, environment, file); got != flag {
		t.Errorf("with a flag set, got %q", got)
	}
	if got := First("", environment, file); got != environment {
		t.Errorf("environment must beat the committed file, got %q", got)
	}
	if got := First("", "", file); got != file {
		t.Errorf("the file is the last resort, got %q", got)
	}
	if got := First("", "", "", "fallback"); got != "fallback" {
		t.Errorf("fallback = %q", got)
	}
}
