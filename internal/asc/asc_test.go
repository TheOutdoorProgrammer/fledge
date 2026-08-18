package asc

import (
	"encoding/json"
	"testing"
)

// keys reports which attribute names a request body would actually carry.
func keys(t *testing.T, attributes deviceAttributes) map[string]bool {
	t.Helper()

	raw, err := json.Marshal(attributes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	present := make(map[string]bool, len(decoded))
	for key := range decoded {
		present[key] = true
	}

	return present
}

// TestUpdateSendsOnlyWhatChanged guards the 409 Apple returns when a PATCH
// carries create-only attributes: "The attribute 'udid' can not be included in
// a 'UPDATE' operation".
func TestUpdateSendsOnlyWhatChanged(t *testing.T) {
	rename := keys(t, deviceAttributes{Name: "Joey's iPhone"})
	for _, forbidden := range []string{"udid", "platform", "status", "deviceClass"} {
		if rename[forbidden] {
			t.Errorf("a rename must not send %q", forbidden)
		}
	}
	if !rename["name"] {
		t.Error("a rename must send name")
	}

	status := keys(t, deviceAttributes{Status: "ENABLED"})
	for _, forbidden := range []string{"udid", "platform", "name"} {
		if status[forbidden] {
			t.Errorf("a status change must not send %q", forbidden)
		}
	}
	if !status["status"] {
		t.Error("a status change must send status")
	}
}

func TestRegistrationSendsEverythingAppleRequires(t *testing.T) {
	create := keys(t, deviceAttributes{
		Name:     "Joey's iPhone",
		UDID:     "00008130-000A14862821401C",
		Platform: "IOS",
	})

	for _, required := range []string{"name", "udid", "platform"} {
		if !create[required] {
			t.Errorf("registration must send %q", required)
		}
	}
}

func TestCapacityCountsDisabledDevicesToo(t *testing.T) {
	// A disabled device still occupies one of the hundred annual slots, so it
	// has to be counted or the remaining figure lies.
	devices := []Device{
		{DeviceType: "IPHONE", Status: "ENABLED"},
		{DeviceType: "IPHONE", Status: "DISABLED"},
		{DeviceType: "IPAD", Status: "ENABLED"},
	}

	counts := map[string]Capacity{}
	for _, device := range devices {
		entry := counts[device.DeviceType]
		entry.Platform, entry.Limit = device.DeviceType, DeviceLimit
		entry.Used++
		counts[device.DeviceType] = entry
	}

	if counts["IPHONE"].Used != 2 {
		t.Errorf("iPhone slots used = %d, want 2 including the disabled one", counts["IPHONE"].Used)
	}
	if got := counts["IPHONE"].Remaining(); got != DeviceLimit-2 {
		t.Errorf("iPhone remaining = %d, want %d", got, DeviceLimit-2)
	}
}
