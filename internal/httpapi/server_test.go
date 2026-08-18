package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theoutdoorprogrammer/fledge/internal/config"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

const testToken = "test-token"

const testInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>dev.example.demo</string>
	<key>CFBundleDisplayName</key><string>Demo App</string>
	<key>CFBundleShortVersionString</key><string>3.4</string>
	<key>CFBundleVersion</key><string>91</string>
	<key>CFBundleSupportedPlatforms</key><array><string>iPhoneOS</string></array>
</dict>
</plist>`

func testIPA(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	writer, err := archive.Create("Payload/Demo.app/Info.plist")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if _, err := writer.Write([]byte(testInfoPlist)); err != nil {
		t.Fatalf("write member: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	return buf.Bytes()
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	cfg := &config.Config{
		PublicURL:   "https://fledge.example",
		UploadToken: testToken,
		Title:       "Fledge",
		MaxUpload:   config.DefaultMaxUpload,
	}

	return New(cfg, st, nil, slog.New(slog.DiscardHandler))
}

func upload(t *testing.T, server *Server) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/api/builds", bytes.NewReader(testIPA(t)))
	request.Header.Set("Authorization", "Bearer "+testToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload = %d, body %s", recorder.Code, recorder.Body)
	}

	var response uploadResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	return response.BuildID
}

func get(t *testing.T, server *Server, path string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

	return recorder
}

func TestUploadRequiresTheToken(t *testing.T) {
	server := newTestServer(t)

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/builds", strings.NewReader("")))

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated upload = %d, want 401", recorder.Code)
	}
}

func TestUploadRejectsSomethingThatIsNotAnIPA(t *testing.T) {
	server := newTestServer(t)

	request := httptest.NewRequest(http.MethodPost, "/api/builds", strings.NewReader("hello"))
	request.Header.Set("Authorization", "Bearer "+testToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("garbage upload = %d, want 400", recorder.Code)
	}
}

// TestEveryPageRenders is the regression guard for template namespace
// collisions: html/template shares one namespace, so a page defining "body"
// silently overwrote every other page until each got its own set.
func TestEveryPageRenders(t *testing.T) {
	server := newTestServer(t)
	buildID := upload(t, server)

	pages := map[string]string{
		"/":                              "Demo App",
		"/a/dev.example.demo":            "Install Demo App",
		"/a/dev.example.demo/" + buildID: "Install Demo App",
		"/a/dev.example.demo/nope":       "Not here",
	}

	for path, want := range pages {
		recorder := get(t, server, path)
		body := recorder.Body.String()

		if strings.Contains(body, "template error") {
			t.Errorf("%s rendered a template error: %s", path, body)
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s does not contain %q", path, want)
		}
	}
}

func TestInstallPagePreservesTheITMSServicesURL(t *testing.T) {
	server := newTestServer(t)
	buildID := upload(t, server)

	page := get(t, server, "/a/dev.example.demo")
	body := page.Body.String()
	want := "itms-services://?action=download-manifest&amp;url=https://fledge.example/a/dev.example.demo/" + buildID + "/manifest.plist"
	if !strings.Contains(body, want) {
		t.Errorf("install page does not contain the iOS installer URL %q", want)
	}
	if strings.Contains(body, "#ZgotmplZ") {
		t.Error("install URL was rejected by html/template")
	}
}

func TestManifestIsServedAsApplePropertyList(t *testing.T) {
	server := newTestServer(t)
	buildID := upload(t, server)

	recorder := get(t, server, "/a/dev.example.demo/"+buildID+"/manifest.plist")
	if recorder.Code != http.StatusOK {
		t.Fatalf("manifest = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/xml" {
		t.Errorf("Content-Type = %q, want text/xml", got)
	}

	body := recorder.Body.String()
	for _, want := range []string{
		"software-package",
		"display-image",
		"full-size-image",
		"<string>dev.example.demo</string>",
		"https://fledge.example/a/dev.example.demo/" + buildID + "/app.ipa",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("manifest is missing %q", want)
		}
	}
}

func TestPackageAndIconAreServable(t *testing.T) {
	server := newTestServer(t)
	buildID := upload(t, server)

	pkg := get(t, server, "/a/dev.example.demo/"+buildID+"/app.ipa")
	if pkg.Code != http.StatusOK {
		t.Errorf("package = %d", pkg.Code)
	}
	if got := pkg.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("package Content-Type = %q", got)
	}

	// The synthetic archive has no icon, so this exercises the placeholder.
	icon := get(t, server, "/a/dev.example.demo/"+buildID+"/icon.png")
	if icon.Code != http.StatusOK {
		t.Errorf("icon = %d", icon.Code)
	}
	if got := icon.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("icon Content-Type = %q", got)
	}
	if icon.Body.Len() == 0 {
		t.Error("icon body is empty")
	}
}

func TestGeneratedWebAssetsAreServable(t *testing.T) {
	server := newTestServer(t)

	for _, name := range []string{"icon-1024.png", "icon-512.png", "icon-180.png", "favicon-32.png", "favicon-16.png"} {
		recorder := get(t, server, "/assets/"+name)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", name, recorder.Code)
		}
		if got := recorder.Header().Get("Content-Type"); got != "image/png" {
			t.Errorf("%s Content-Type = %q, want image/png", name, got)
		}
		if recorder.Body.Len() == 0 {
			t.Errorf("%s body is empty", name)
		}
	}
}

func TestDeviceCookieRoundTrips(t *testing.T) {
	server := newTestServer(t)
	if err := server.store.PutDevice(&store.Device{UDID: "00008030-001A2B3C0E88802E", Name: "Test iPhone"}); err != nil {
		t.Fatalf("put device: %v", err)
	}

	recorder := httptest.NewRecorder()
	server.setDeviceCookie(recorder, "00008030-001A2B3C0E88802E")

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range recorder.Result().Cookies() {
		request.AddCookie(cookie)
	}

	device := server.deviceFromCookie(request)
	if device == nil {
		t.Fatal("a cookie Fledge just set did not resolve to a device")
	}
	if device.Name != "Test iPhone" {
		t.Errorf("Name = %q", device.Name)
	}
}

func TestDeviceCookieRejectsATamperedSignature(t *testing.T) {
	server := newTestServer(t)
	if err := server.store.PutDevice(&store.Device{UDID: "00008030-001A2B3C0E88802E", Name: "Test iPhone"}); err != nil {
		t.Fatalf("put device: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: deviceCookie, Value: "00008030-001A2B3C0E88802E.deadbeef"})

	if device := server.deviceFromCookie(request); device != nil {
		t.Error("a forged cookie was accepted")
	}
}
