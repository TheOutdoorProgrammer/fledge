package enroll

import (
	"regexp"
	"strings"
	"testing"

	"howett.net/plist"
)

// canonicalUUID is the dashed form Apple requires. A bare hex string parses as
// a plist just fine and then fails on the device as "Invalid Profile".
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestProfileUsesACanonicalUUID(t *testing.T) {
	document, err := Profile(Options{
		CallbackURL: "https://fledge.example/enroll/callback?c=abc",
		Challenge:   "abc",
	})
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	var decoded struct {
		PayloadUUID       string `plist:"PayloadUUID"`
		PayloadType       string `plist:"PayloadType"`
		PayloadVersion    int    `plist:"PayloadVersion"`
		PayloadIdentifier string `plist:"PayloadIdentifier"`
		PayloadContent    struct {
			URL              string   `plist:"URL"`
			Challenge        string   `plist:"Challenge"`
			DeviceAttributes []string `plist:"DeviceAttributes"`
		} `plist:"PayloadContent"`
	}
	if _, err := plist.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !canonicalUUID.MatchString(decoded.PayloadUUID) {
		t.Errorf("PayloadUUID = %q, want a dashed RFC 4122 v4 identifier", decoded.PayloadUUID)
	}
	if decoded.PayloadType != "Profile Service" {
		t.Errorf("PayloadType = %q", decoded.PayloadType)
	}
	if decoded.PayloadVersion != 1 {
		t.Errorf("PayloadVersion = %d, want 1", decoded.PayloadVersion)
	}
	if decoded.PayloadIdentifier != "zone.fledge.enroll" {
		t.Errorf("PayloadIdentifier = %q", decoded.PayloadIdentifier)
	}
	if decoded.PayloadContent.Challenge != "abc" {
		t.Errorf("Challenge = %q", decoded.PayloadContent.Challenge)
	}
	if !strings.Contains(decoded.PayloadContent.URL, "/enroll/callback") {
		t.Errorf("URL = %q", decoded.PayloadContent.URL)
	}
	if len(decoded.PayloadContent.DeviceAttributes) == 0 {
		t.Error("no DeviceAttributes requested")
	}

	var sawUDID bool
	for _, attribute := range decoded.PayloadContent.DeviceAttributes {
		if attribute == "UDID" {
			sawUDID = true
		}
	}
	if !sawUDID {
		t.Error("the profile does not ask for UDID, which is the whole point")
	}
}

func TestEveryProfileGetsItsOwnUUID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 25; i++ {
		uuid, err := newUUID()
		if err != nil {
			t.Fatalf("newUUID: %v", err)
		}
		if !canonicalUUID.MatchString(uuid) {
			t.Fatalf("newUUID produced %q", uuid)
		}
		if seen[uuid] {
			t.Fatalf("newUUID repeated %q", uuid)
		}
		seen[uuid] = true
	}
}
