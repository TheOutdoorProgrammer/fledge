// Package enroll implements Apple's OTA Profile Service, which is the only way
// to read a device's UDID from a browser.
//
// Apple documents this in the archived Over-the-Air Profile Delivery guide and
// nowhere in the current device management documentation. It works, and every
// UDID-capture service relies on it, but it is undocumented legacy surface that
// Apple could withdraw without a deprecation notice.
package enroll

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"howett.net/plist"
)

// ContentType is what a configuration profile must be served as. Anything else
// and Safari offers to display the file instead of handing it to iOS.
const ContentType = "application/x-apple-aspen-config"

// Attributes Fledge asks the device for. UDID is the one that matters; the rest
// make the enrolled device recognisable in a list.
var requestedAttributes = []string{"UDID", "PRODUCT", "VERSION", "SERIAL", "DEVICE_NAME"}

// payload is the Profile Service dictionary iOS acts on.
type payload struct {
	URL              string   `plist:"URL"`
	DeviceAttributes []string `plist:"DeviceAttributes"`
	Challenge        string   `plist:"Challenge"`
}

// profile is the outer configuration profile.
type profile struct {
	PayloadContent      payload `plist:"PayloadContent"`
	PayloadOrganization string  `plist:"PayloadOrganization"`
	PayloadDisplayName  string  `plist:"PayloadDisplayName"`
	PayloadDescription  string  `plist:"PayloadDescription"`
	PayloadIdentifier   string  `plist:"PayloadIdentifier"`
	PayloadUUID         string  `plist:"PayloadUUID"`
	PayloadType         string  `plist:"PayloadType"`
	PayloadVersion      int     `plist:"PayloadVersion"`
}

// Request is one in-flight enrolment.
type Request struct {
	Challenge string
	Issued    time.Time
}

// Expired reports whether iOS has almost certainly discarded the download. The
// device drops a queued profile after roughly eight minutes, so a challenge
// outliving that only serves to keep a replay window open.
func (r Request) Expired(now time.Time) bool {
	return now.Sub(r.Issued) > 10*time.Minute
}

// NewChallenge mints the value that ties a returned UDID back to the browser
// session that asked for it.
func NewChallenge() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// newUUID returns an RFC 4122 version 4 identifier. iOS rejects the whole
// profile as "Invalid Profile" if PayloadUUID is not in canonical dashed form,
// which is not a validation error it explains.
func newUUID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

// Options configures one generated profile.
type Options struct {
	CallbackURL  string
	Challenge    string
	Organization string
	UUID         string
}

// Profile renders the unsigned configuration profile. iOS installs it either
// way but labels it "Not Signed", so callers should sign the result.
func Profile(opts Options) ([]byte, error) {
	if opts.CallbackURL == "" {
		return nil, fmt.Errorf("enroll: callback url is required")
	}

	organization := opts.Organization
	if organization == "" {
		organization = "Fledge"
	}

	uuid := opts.UUID
	if uuid == "" {
		generated, err := newUUID()
		if err != nil {
			return nil, err
		}
		uuid = generated
	}

	document := profile{
		PayloadContent: payload{
			URL:              opts.CallbackURL,
			DeviceAttributes: requestedAttributes,
			Challenge:        opts.Challenge,
		},
		PayloadOrganization: organization,
		PayloadDisplayName:  organization + " device registration",
		PayloadDescription:  "Reads this device's identifier so builds can be signed for it. It installs nothing and grants no ongoing access.",
		PayloadIdentifier:   "zone.fledge.enroll",
		PayloadUUID:         uuid,
		PayloadType:         "Profile Service",
		PayloadVersion:      1,
	}

	var out bytes.Buffer
	encoder := plist.NewEncoderForFormat(&out, plist.XMLFormat)
	encoder.Indent("\t")
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("enroll: encode profile: %w", err)
	}

	return out.Bytes(), nil
}
