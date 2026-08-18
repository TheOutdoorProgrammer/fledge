package ipa

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"howett.net/plist"
)

// ErrNotAnIPA is returned when the archive contains no Payload/<Name>.app.
var ErrNotAnIPA = errors.New("archive does not contain an iOS application payload")

// MaxMemberSize bounds each archive member Fledge reads, so a decompression
// bomb cannot pose as an Info.plist.
const MaxMemberSize = 8 << 20

// App is everything Fledge needs to know about a build, read from the IPA
// rather than supplied by whoever uploaded it.
type App struct {
	BundleID   string   `json:"bundle_id"`
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Build      string   `json:"build"`
	MinimumOS  string   `json:"minimum_os,omitempty"`
	Platforms  []string `json:"platforms,omitempty"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
	Profile    *Profile `json:"profile,omitempty"`
	Icon       []byte   `json:"-"`
	BundlePath string   `json:"-"`
}

// infoPlist is the subset of Info.plist that Fledge reads.
type infoPlist struct {
	BundleIdentifier   string   `plist:"CFBundleIdentifier"`
	BundleName         string   `plist:"CFBundleName"`
	BundleDisplayName  string   `plist:"CFBundleDisplayName"`
	ShortVersionString string   `plist:"CFBundleShortVersionString"`
	BundleVersion      string   `plist:"CFBundleVersion"`
	MinimumOSVersion   string   `plist:"MinimumOSVersion"`
	SupportedPlatforms []string `plist:"CFBundleSupportedPlatforms"`
	Icons              struct {
		Primary struct {
			IconFiles []string `plist:"CFBundleIconFiles"`
		} `plist:"CFBundlePrimaryIcon"`
	} `plist:"CFBundleIcons"`
}

// Open reads an IPA from disk.
func Open(name string) (*App, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	return Read(file, stat.Size())
}

// Read inspects an IPA. The reader is consumed twice — once to hash the bytes
// and once to walk the zip directory — so it must support seeking.
func Read(r io.ReaderAt, size int64) (*App, error) {
	sum, err := hash(r, size)
	if err != nil {
		return nil, err
	}

	archive, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open ipa: %w", err)
	}

	bundle, err := locateBundle(archive)
	if err != nil {
		return nil, err
	}

	app := &App{Size: size, SHA256: sum, BundlePath: bundle}

	info, err := readInfoPlist(archive, bundle)
	if err != nil {
		return nil, err
	}
	app.BundleID = info.BundleIdentifier
	app.Name = firstNonEmpty(info.BundleDisplayName, info.BundleName, path.Base(bundle))
	app.Version = info.ShortVersionString
	app.Build = info.BundleVersion
	app.MinimumOS = info.MinimumOSVersion
	app.Platforms = info.SupportedPlatforms

	if der, err := readMember(archive, bundle+"/embedded.mobileprovision"); err == nil {
		profile, err := ParseProfile(der)
		if err != nil {
			return nil, err
		}
		app.Profile = profile
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	app.Icon = extractIcon(archive, bundle, info.Icons.Primary.IconFiles)

	return app, nil
}

// Identifier is the stable key Fledge files a build under. Bundle ID alone is
// not enough because the same app is uploaded repeatedly.
func (a *App) Identifier() string {
	return a.BundleID + "@" + a.Version + "+" + a.Build
}

// RequiresDeveloperMode reports whether the device must have Developer Mode
// enabled before the installed app will launch. Only the debugger entitlement
// on a development build triggers it.
func (a *App) RequiresDeveloperMode() bool {
	return a.Profile != nil && a.Profile.Type == TypeDevelopment
}

func hash(r io.ReaderAt, size int64) (string, error) {
	digest := sha256.New()
	if _, err := io.Copy(digest, io.NewSectionReader(r, 0, size)); err != nil {
		return "", fmt.Errorf("hash ipa: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// locateBundle finds the Payload/<Name>.app directory by prefix rather than by
// looking for a directory entry, because a zip is not obliged to carry one.
// Anything deeper is a framework or an extension, not the application.
func locateBundle(archive *zip.Reader) (string, error) {
	for _, file := range archive.File {
		parts := strings.Split(file.Name, "/")
		if len(parts) < 2 || parts[0] != "Payload" || !strings.HasSuffix(parts[1], ".app") {
			continue
		}
		return parts[0] + "/" + parts[1], nil
	}
	return "", ErrNotAnIPA
}

func readInfoPlist(archive *zip.Reader, bundle string) (*infoPlist, error) {
	raw, err := readMember(archive, bundle+"/Info.plist")
	if err != nil {
		return nil, fmt.Errorf("read Info.plist: %w", err)
	}

	var info infoPlist
	if _, err := plist.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode Info.plist: %w", err)
	}
	if info.BundleIdentifier == "" {
		return nil, errors.New("no CFBundleIdentifier in Info.plist")
	}

	return &info, nil
}

// readMember returns one file from the archive, wrapping a missing entry as
// os.ErrNotExist so callers can distinguish absent from malformed.
func readMember(archive *zip.Reader, name string) ([]byte, error) {
	file, err := archive.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, os.ErrNotExist)
	}
	defer func() { _ = file.Close() }()

	return io.ReadAll(io.LimitReader(file, MaxMemberSize))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
