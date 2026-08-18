// Package manifest builds the property list iOS fetches when a device follows
// an itms-services URL.
package manifest

import (
	"bytes"
	"fmt"

	"howett.net/plist"
)

// Asset kinds iOS recognises inside a manifest item.
const (
	KindSoftwarePackage = "software-package"
	KindDisplayImage    = "display-image"
	KindFullSizeImage   = "full-size-image"
)

// Asset is one downloadable resource. Every URL must be HTTPS and must present
// a certificate the device already trusts; iOS gives no diagnostic when it
// refuses one, the install simply never starts.
type Asset struct {
	Kind string `plist:"kind"`
	URL  string `plist:"url"`
}

// Platform identifiers iOS accepts in manifest metadata.
const (
	PlatformIOS      = "com.apple.platform.iphoneos"
	PlatformWatchOS  = "com.apple.platform.watchos"
	PlatformVisionOS = "com.apple.platform.xros"
)

// Metadata is what iOS shows in the install confirmation prompt. The bundle
// identifier must match the one inside the package, which iOS has enforced
// since iOS 9.
type Metadata struct {
	BundleIdentifier   string `plist:"bundle-identifier"`
	BundleVersion      string `plist:"bundle-version"`
	Kind               string `plist:"kind"`
	Title              string `plist:"title"`
	Subtitle           string `plist:"subtitle,omitempty"`
	PlatformIdentifier string `plist:"platform-identifier,omitempty"`
}

// PlatformFor maps a CFBundleSupportedPlatforms entry onto the identifier the
// manifest uses, returning an empty string when the platform is unrecognised so
// the key is omitted rather than guessed.
func PlatformFor(supported []string) string {
	for _, platform := range supported {
		switch platform {
		case "iPhoneOS":
			return PlatformIOS
		case "WatchOS":
			return PlatformWatchOS
		case "XROS":
			return PlatformVisionOS
		}
	}
	return ""
}

type item struct {
	Assets   []Asset  `plist:"assets"`
	Metadata Metadata `plist:"metadata"`
}

type document struct {
	Items []item `plist:"items"`
}

// Build describes one installable build to Manifest.
type Build struct {
	BundleIdentifier   string
	BundleVersion      string
	Title              string
	Subtitle           string
	PlatformIdentifier string
	PackageURL         string
	DisplayImageURL    string
	FullSizeImageURL   string
}

// Manifest renders the XML property list for a single build. Both image URLs
// are required: Xcode's own generator rejects a manifest without them, so
// callers must supply a placeholder rather than omit the asset.
func Manifest(build Build) ([]byte, error) {
	switch {
	case build.BundleIdentifier == "":
		return nil, fmt.Errorf("manifest: bundle identifier is required")
	case build.PackageURL == "":
		return nil, fmt.Errorf("manifest: package url is required")
	case build.DisplayImageURL == "":
		return nil, fmt.Errorf("manifest: display image url is required")
	case build.FullSizeImageURL == "":
		return nil, fmt.Errorf("manifest: full size image url is required")
	}

	doc := document{Items: []item{{
		Assets: []Asset{
			{Kind: KindSoftwarePackage, URL: build.PackageURL},
			{Kind: KindDisplayImage, URL: build.DisplayImageURL},
			{Kind: KindFullSizeImage, URL: build.FullSizeImageURL},
		},
		Metadata: Metadata{
			BundleIdentifier:   build.BundleIdentifier,
			BundleVersion:      build.BundleVersion,
			Kind:               "software",
			Title:              build.Title,
			Subtitle:           build.Subtitle,
			PlatformIdentifier: build.PlatformIdentifier,
		},
	}}}

	var out bytes.Buffer
	encoder := plist.NewEncoderForFormat(&out, plist.XMLFormat)
	encoder.Indent("\t")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("manifest: encode: %w", err)
	}

	return out.Bytes(), nil
}

// InstallURL wraps a manifest URL in the scheme iOS hands to the installer.
// The manifest URL is not escaped: iOS parses the remainder of the string as
// one URL, and percent-encoding it stops the install silently.
func InstallURL(manifestURL string) string {
	return "itms-services://?action=download-manifest&url=" + manifestURL
}
