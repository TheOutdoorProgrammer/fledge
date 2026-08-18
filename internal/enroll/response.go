package enroll

import (
	"errors"
	"fmt"
	"strings"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"
)

// ErrChallengeMismatch means the device answered with a challenge Fledge did
// not issue, so the response cannot be tied to a browser session.
var ErrChallengeMismatch = errors.New("enroll: challenge does not match")

// Device is what the device told Fledge about itself.
type Device struct {
	UDID      string `plist:"UDID"`
	Product   string `plist:"PRODUCT"`
	Version   string `plist:"VERSION"`
	Serial    string `plist:"SERIAL"`
	Name      string `plist:"DEVICE_NAME"`
	Challenge string `plist:"CHALLENGE"`
}

// DisplayName is the device's own name when it gave one, and a description
// built from the hardware otherwise.
func (d Device) DisplayName() string {
	if d.Name != "" {
		return d.Name
	}
	if d.Product != "" {
		return ModelName(d.Product)
	}
	return "Unknown device"
}

// ParseResponse decodes the CMS envelope the device posts back. Apple says the
// device certificate's dates carry no meaning, so trust rests on the challenge.
func ParseResponse(body []byte, expected string) (*Device, error) {
	signed, err := pkcs7.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("enroll: parse response envelope: %w", err)
	}
	if err := signed.Verify(); err != nil {
		return nil, fmt.Errorf("enroll: response signature: %w", err)
	}

	var device Device
	if _, err := plist.Unmarshal(signed.Content, &device); err != nil {
		return nil, fmt.Errorf("enroll: decode response plist: %w", err)
	}
	if device.UDID == "" {
		return nil, errors.New("enroll: response carries no UDID")
	}
	if expected != "" && device.Challenge != expected {
		return nil, ErrChallengeMismatch
	}

	return &device, nil
}

// ModelName turns an identifier such as iPhone17,1 into something a human
// recognises. Apple ships no lookup for this, so unknown hardware degrades to
// the family plus the raw identifier rather than guessing a marketing name.
func ModelName(product string) string {
	family, _, ok := strings.Cut(product, ",")
	if !ok {
		return product
	}

	switch {
	case strings.HasPrefix(family, "iPhone"):
		return "iPhone (" + product + ")"
	case strings.HasPrefix(family, "iPad"):
		return "iPad (" + product + ")"
	case strings.HasPrefix(family, "iPod"):
		return "iPod touch (" + product + ")"
	case strings.HasPrefix(family, "Watch"):
		return "Apple Watch (" + product + ")"
	default:
		return product
	}
}

// Platform maps a hardware identifier onto the platform value the App Store
// Connect API expects when registering a device.
func Platform(product string) string {
	if strings.HasPrefix(product, "Mac") {
		return "MAC_OS"
	}
	return "IOS"
}
