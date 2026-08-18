package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// uploadVersion publishes a synthetic build so a changelog has something in it.
func uploadVersion(t *testing.T, server *Server, version, build, notes string) {
	t.Helper()

	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	writer, err := archive.Create("Payload/Demo.app/Info.plist")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>dev.example.demo</string>
	<key>CFBundleDisplayName</key><string>Demo App</string>
	<key>CFBundleShortVersionString</key><string>%s</string>
	<key>CFBundleVersion</key><string>%s</string>
</dict>
</plist>`, version, build)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("write member: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	target := "/api/builds?notes=" + url.QueryEscape(notes)
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(buf.Bytes()))
	request.Header.Set("Authorization", "Bearer "+testToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload %s = %d: %s", version, recorder.Code, recorder.Body)
	}
}

func latest(t *testing.T, server *Server, query string) latestResponse {
	t.Helper()

	recorder := get(t, server, "/api/v1/apps/dev.example.demo/latest"+query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("latest%s = %d: %s", query, recorder.Code, recorder.Body)
	}

	var response latestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return response
}

// TestLatestIsPublic matters because the app asking is already installed, and
// an authenticated check would need a credential shipped inside the binary.
func TestLatestIsPublic(t *testing.T) {
	server := newTestServer(t)
	uploadVersion(t, server, "1.0", "1", "first release")

	if response := latest(t, server, ""); response.Version != "1.0" {
		t.Errorf("version = %q", response.Version)
	}
}

func TestLatestReportsOnlyWhatIsNewer(t *testing.T) {
	server := newTestServer(t)
	uploadVersion(t, server, "1.0", "1", "first release")
	uploadVersion(t, server, "1.1", "2", "## Fixed\n\n- the launch crash")
	uploadVersion(t, server, "1.2", "3", "## Added\n\n- offline mode")

	current := latest(t, server, "?build=3")
	if current.UpdateAvailable {
		t.Error("the newest build was told an update is available")
	}
	if len(current.Changelog) != 0 {
		t.Errorf("changelog for the newest build = %d entries, want none", len(current.Changelog))
	}

	behind := latest(t, server, "?build=1")
	if !behind.UpdateAvailable {
		t.Error("a build two releases behind was told it is current")
	}
	if len(behind.Changelog) != 2 {
		t.Fatalf("changelog = %d entries, want the two releases since build 1", len(behind.Changelog))
	}
	if behind.Changelog[0].Build != "3" || behind.Changelog[1].Build != "2" {
		t.Errorf("changelog is not newest first: %+v", behind.Changelog)
	}

	// Markdown has to survive the round trip, since the app renders it.
	if behind.Changelog[1].Notes != "## Fixed\n\n- the launch crash" {
		t.Errorf("notes lost their formatting: %q", behind.Changelog[1].Notes)
	}
}

func TestLatestGivesTheWholeHistoryToAStranger(t *testing.T) {
	server := newTestServer(t)
	uploadVersion(t, server, "1.0", "1", "first release")
	uploadVersion(t, server, "1.1", "2", "second release")

	// A build this server never published is a sideload or a fresh install, and
	// both are better served the full history than an empty one.
	response := latest(t, server, "?build=999")
	if len(response.Changelog) != 2 {
		t.Errorf("changelog for an unknown build = %d entries, want all of them", len(response.Changelog))
	}
}

func TestLatestOmitsReleasesWithNothingToSay(t *testing.T) {
	server := newTestServer(t)
	uploadVersion(t, server, "1.0", "1", "first release")
	uploadVersion(t, server, "1.1", "2", "")

	response := latest(t, server, "?build=1")
	for _, released := range response.Changelog {
		if released.Notes == "" {
			t.Error("an entry with no notes was included, which renders as a blank row")
		}
	}
}

func TestLatestIs404ForAnUnknownApp(t *testing.T) {
	server := newTestServer(t)

	recorder := get(t, server, "/api/v1/apps/dev.example.nothing/latest")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("unknown app = %d, want 404", recorder.Code)
	}
}
