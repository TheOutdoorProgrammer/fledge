// Package ipa reads iOS application archives without unpacking them to disk.
package ipa

import (
	"fmt"
	"time"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"
)

// Type classifies a provisioning profile by how Apple permits the signed
// application to be installed.
type Type string

const (
	// TypeDevelopment installs only on listed devices and keeps the debugger
	// entitlement, which is what forces Developer Mode on iOS 16 and later.
	TypeDevelopment Type = "development"
	// TypeAdHoc installs only on listed devices with the debugger entitlement
	// stripped.
	TypeAdHoc Type = "ad-hoc"
	// TypeEnterprise installs on any device and requires an Apple Developer
	// Enterprise Program membership.
	TypeEnterprise Type = "enterprise"
	// TypeAppStore carries no device list and cannot be installed over the air.
	TypeAppStore Type = "app-store"
)

// InstallsOverTheAir reports whether a profile of this type can be delivered
// through an itms-services manifest at all.
func (t Type) InstallsOverTheAir() bool {
	return t != TypeAppStore
}

// Profile is the decoded contents of an embedded.mobileprovision.
type Profile struct {
	Name                 string    `json:"name"`
	UUID                 string    `json:"uuid"`
	AppIDName            string    `json:"app_id_name"`
	TeamName             string    `json:"team_name"`
	TeamID               string    `json:"team_id"`
	Type                 Type      `json:"type"`
	Created              time.Time `json:"created"`
	Expires              time.Time `json:"expires"`
	Devices              []string  `json:"devices,omitempty"`
	ProvisionsAllDevices bool      `json:"provisions_all_devices"`
	GetTaskAllow         bool      `json:"get_task_allow"`
}

// rawProfile mirrors the subset of the profile plist that Fledge reads. Apple
// ships far more keys than this; the rest are deliberately ignored so that a
// new key cannot break decoding.
type rawProfile struct {
	Name                 string    `plist:"Name"`
	UUID                 string    `plist:"UUID"`
	AppIDName            string    `plist:"AppIDName"`
	TeamName             string    `plist:"TeamName"`
	TeamIdentifier       []string  `plist:"TeamIdentifier"`
	CreationDate         time.Time `plist:"CreationDate"`
	ExpirationDate       time.Time `plist:"ExpirationDate"`
	ProvisionedDevices   []string  `plist:"ProvisionedDevices"`
	ProvisionsAllDevices bool      `plist:"ProvisionsAllDevices"`
	Entitlements         struct {
		GetTaskAllow bool `plist:"get-task-allow"`
	} `plist:"Entitlements"`
}

// ParseProfile decodes an embedded.mobileprovision. The file is a CMS
// SignedData envelope wrapping an XML plist, so the signature has to come off
// before the plist is readable.
func ParseProfile(der []byte) (*Profile, error) {
	signed, err := pkcs7.Parse(der)
	if err != nil {
		return nil, fmt.Errorf("parse provisioning profile envelope: %w", err)
	}

	var raw rawProfile
	if _, err := plist.Unmarshal(signed.Content, &raw); err != nil {
		return nil, fmt.Errorf("decode provisioning profile plist: %w", err)
	}

	profile := &Profile{
		Name:                 raw.Name,
		UUID:                 raw.UUID,
		AppIDName:            raw.AppIDName,
		TeamName:             raw.TeamName,
		Created:              raw.CreationDate,
		Expires:              raw.ExpirationDate,
		Devices:              raw.ProvisionedDevices,
		ProvisionsAllDevices: raw.ProvisionsAllDevices,
		GetTaskAllow:         raw.Entitlements.GetTaskAllow,
	}
	if len(raw.TeamIdentifier) > 0 {
		profile.TeamID = raw.TeamIdentifier[0]
	}
	profile.Type = classify(raw)

	return profile, nil
}

// classify derives the distribution type, which Apple does not stamp in a field
// of its own. get-task-allow is what separates development from Ad Hoc: only a
// development profile carries the entitlement that lets a debugger attach.
func classify(raw rawProfile) Type {
	if raw.ProvisionsAllDevices {
		return TypeEnterprise
	}
	if len(raw.ProvisionedDevices) == 0 {
		return TypeAppStore
	}
	if raw.Entitlements.GetTaskAllow {
		return TypeDevelopment
	}
	return TypeAdHoc
}

// Authorizes reports whether a device may install a build signed with this
// profile. Comparison is case-insensitive because UDIDs reach Fledge from
// several sources that disagree about the case of the hex digits.
func (p *Profile) Authorizes(udid string) bool {
	if p.ProvisionsAllDevices {
		return true
	}
	for _, device := range p.Devices {
		if equalFoldASCII(device, udid) {
			return true
		}
	}
	return false
}

// Expired reports whether the profile is no longer valid at the given instant.
// An expired profile does not stop an already-installed app from having been
// installed, but it does stop it from launching.
func (p *Profile) Expired(now time.Time) bool {
	return !p.Expires.IsZero() && now.After(p.Expires)
}

// ExpiresWithin reports whether the profile lapses inside the given window,
// which is how the install page decides to warn before a build goes dead.
func (p *Profile) ExpiresWithin(now time.Time, window time.Duration) bool {
	if p.Expires.IsZero() {
		return false
	}
	return p.Expires.Before(now.Add(window))
}

// equalFoldASCII compares two ASCII strings without allocating. UDIDs are hex
// and dashes, so the general Unicode folding in strings.EqualFold is more
// machinery than the comparison needs.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
