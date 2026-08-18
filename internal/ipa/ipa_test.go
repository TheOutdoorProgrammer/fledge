package ipa

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

const infoPlistXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>dev.example.demo</string>
	<key>CFBundleName</key><string>Demo</string>
	<key>CFBundleDisplayName</key><string>Demo App</string>
	<key>CFBundleShortVersionString</key><string>2.1</string>
	<key>CFBundleVersion</key><string>117</string>
	<key>MinimumOSVersion</key><string>17.0</string>
	<key>CFBundleSupportedPlatforms</key><array><string>iPhoneOS</string></array>
</dict>
</plist>`

func syntheticIPA(t *testing.T) ([]byte, int64) {
	t.Helper()

	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)

	for _, member := range []struct{ name, body string }{
		{"Payload/Demo.app/Info.plist", infoPlistXML},
		{"Payload/Demo.app/Demo", "mach-o"},
		{"Payload/Demo.app/Frameworks/Thing.framework/Info.plist", infoPlistXML},
	} {
		writer, err := archive.Create(member.name)
		if err != nil {
			t.Fatalf("create %s: %v", member.name, err)
		}
		if _, err := writer.Write([]byte(member.body)); err != nil {
			t.Fatalf("write %s: %v", member.name, err)
		}
	}

	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	return buf.Bytes(), int64(buf.Len())
}

func TestReadFindsTheApplicationBundle(t *testing.T) {
	raw, size := syntheticIPA(t)

	app, err := Read(bytes.NewReader(raw), size)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if app.BundlePath != "Payload/Demo.app" {
		t.Errorf("BundlePath = %q, want Payload/Demo.app", app.BundlePath)
	}
	if app.BundleID != "dev.example.demo" {
		t.Errorf("BundleID = %q", app.BundleID)
	}
	if app.Name != "Demo App" {
		t.Errorf("Name = %q, want the display name to win", app.Name)
	}
	if app.Version != "2.1" || app.Build != "117" {
		t.Errorf("Version/Build = %q/%q", app.Version, app.Build)
	}
	if app.SHA256 == "" {
		t.Error("SHA256 is empty")
	}
	if app.Identifier() != "dev.example.demo@2.1+117" {
		t.Errorf("Identifier = %q", app.Identifier())
	}
}

func TestReadRejectsAnArchiveWithNoPayload(t *testing.T) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	writer, err := archive.Create("README.txt")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if _, err := writer.Write([]byte("not an app")); err != nil {
		t.Fatalf("write member: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	if _, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil {
		t.Fatal("expected an error for an archive with no Payload")
	}
}

func TestClassifyDistinguishesDevelopmentFromAdHoc(t *testing.T) {
	adHoc := rawProfile{ProvisionedDevices: []string{"00008030-ABC"}}
	if got := classify(adHoc); got != TypeAdHoc {
		t.Errorf("a device list without get-task-allow = %q, want ad-hoc", got)
	}

	development := adHoc
	development.Entitlements.GetTaskAllow = true
	if got := classify(development); got != TypeDevelopment {
		t.Errorf("a device list with get-task-allow = %q, want development", got)
	}

	if got := classify(rawProfile{ProvisionsAllDevices: true}); got != TypeEnterprise {
		t.Errorf("ProvisionsAllDevices = %q, want enterprise", got)
	}

	if got := classify(rawProfile{}); got != TypeAppStore {
		t.Errorf("no devices at all = %q, want app-store", got)
	}
}

func TestAuthorizesIgnoresUDIDCase(t *testing.T) {
	profile := &Profile{Devices: []string{"00008030-001A2B3C0E88802E"}}

	if !profile.Authorizes("00008030-001a2b3c0e88802e") {
		t.Error("a lowercase UDID should match its uppercase registration")
	}
	if profile.Authorizes("00008030-DEADBEEFDEADBEEF") {
		t.Error("an unregistered UDID must not be authorized")
	}

	enterprise := &Profile{ProvisionsAllDevices: true}
	if !enterprise.Authorizes("anything") {
		t.Error("an enterprise profile authorizes every device")
	}
}

// TestReadRealIPA runs against a build exported by Xcode. It is skipped unless
// FLEDGE_TEST_IPA points at one, because the fixture cannot be committed.
func TestReadRealIPA(t *testing.T) {
	path := os.Getenv("FLEDGE_TEST_IPA")
	if path == "" {
		t.Skip("set FLEDGE_TEST_IPA to an exported .ipa to run this")
	}

	app, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}

	if app.BundleID == "" {
		t.Error("no bundle identifier")
	}
	if app.Profile == nil {
		t.Fatal("no embedded provisioning profile")
	}
	if app.Profile.Expires.IsZero() {
		t.Error("profile has no expiry date")
	}

	t.Logf("%s %s (%s) build=%s profile=%s team=%s expires=%s devices=%d icon=%dB",
		app.Name, app.Version, app.BundleID, app.Build,
		app.Profile.Type, app.Profile.TeamName,
		app.Profile.Expires.Format("2006-01-02"), len(app.Profile.Devices), len(app.Icon))

	// The CgBI conversion is only verifiable by eye, so make it easy to look at.
	if out := os.Getenv("FLEDGE_TEST_ICON_OUT"); out != "" && len(app.Icon) > 0 {
		if err := os.WriteFile(out, app.Icon, 0o600); err != nil {
			t.Fatalf("write icon: %v", err)
		}
		t.Logf("icon written to %s", out)
	}
}
