package httpapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"howett.net/plist"

	"github.com/theoutdoorprogrammer/fledge/internal/enroll"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

const enrollToken = "enroll-token"

// selfSigned stands in for the device's Apple-issued certificate, which is all
// the callback needs since trust rests on the challenge rather than the chain.
func selfSigned(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test device"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	marshalled, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: marshalled})
}

func enrollServer(t *testing.T) *Server {
	t.Helper()

	server := newTestServer(t)
	server.cfg.Enroll.Token = enrollToken

	return server
}

// challengeFromProfile pulls the challenge out of a generated profile the same
// way a device would.
func challengeFromProfile(t *testing.T, document []byte) string {
	t.Helper()

	var profile struct {
		PayloadContent struct {
			Challenge string `plist:"Challenge"`
			URL       string `plist:"URL"`
		} `plist:"PayloadContent"`
	}
	if _, err := plist.Unmarshal(document, &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.PayloadContent.Challenge == "" {
		t.Fatal("profile carries no challenge")
	}
	if !strings.Contains(profile.PayloadContent.URL, "/enroll/callback") {
		t.Errorf("callback URL = %q", profile.PayloadContent.URL)
	}

	return profile.PayloadContent.Challenge
}

// deviceAnswer builds the signed plist a device posts back.
func deviceAnswer(t *testing.T, challenge, udid string) []byte {
	t.Helper()

	answer := map[string]string{
		"UDID":        udid,
		"PRODUCT":     "iPhone17,1",
		"VERSION":     "26.0",
		"SERIAL":      "ABC123XYZ",
		"DEVICE_NAME": "Test iPhone",
		"CHALLENGE":   challenge,
	}
	body, err := plist.Marshal(answer, plist.XMLFormat)
	if err != nil {
		t.Fatalf("encode answer: %v", err)
	}

	certPEM, keyPEM := selfSigned(t)
	signed, err := enroll.Sign(body, certPEM, keyPEM)
	if err != nil {
		t.Fatalf("sign answer: %v", err)
	}

	return signed
}

func TestEnrolmentIsClosedWithoutAToken(t *testing.T) {
	server := newTestServer(t)

	recorder := get(t, server, "/enroll")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("GET /enroll with enrolment disabled = %d, want 404", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Registration is closed") {
		t.Error("the page does not explain that enrolment is off")
	}
}

func TestEnrolmentRejectsTheWrongToken(t *testing.T) {
	server := enrollServer(t)

	if recorder := get(t, server, "/enroll?t=nope"); recorder.Code != http.StatusNotFound {
		t.Errorf("GET /enroll with a bad token = %d, want 404", recorder.Code)
	}
	if recorder := get(t, server, "/enroll/profile.mobileconfig"); recorder.Code != http.StatusNotFound {
		t.Errorf("profile download without a token = %d, want 404", recorder.Code)
	}
}

func TestEnrolmentRoundTrip(t *testing.T) {
	server := enrollServer(t)
	const udid = "00008140-001A2B3C0E88802E"

	page := get(t, server, "/enroll?t="+enrollToken)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /enroll = %d", page.Code)
	}

	profile := get(t, server, "/enroll/profile.mobileconfig?t="+enrollToken)
	if profile.Code != http.StatusOK {
		t.Fatalf("profile download = %d", profile.Code)
	}
	if got := profile.Header().Get("Content-Type"); got != enroll.ContentType {
		t.Errorf("Content-Type = %q, want %q", got, enroll.ContentType)
	}

	challenge := challengeFromProfile(t, profile.Body.Bytes())

	callback := httptest.NewRequest(http.MethodPost,
		"/enroll/callback?c="+url.QueryEscape(challenge),
		strings.NewReader(string(deviceAnswer(t, challenge, udid))))
	answered := httptest.NewRecorder()
	server.ServeHTTP(answered, callback)

	// 301 specifically, and no Content-Type: iOS reports a 302 here as "Invalid
	// Profile" even after it has already handed over the attributes.
	if answered.Code != http.StatusMovedPermanently {
		t.Fatalf("callback = %d, want 301: %s", answered.Code, answered.Body)
	}
	if got := answered.Header().Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want it absent on the redirect", got)
	}
	if answered.Body.Len() != 0 {
		t.Errorf("redirect carried a %d byte body, want none", answered.Body.Len())
	}

	location := answered.Header().Get("Location")
	if !strings.HasPrefix(location, "https://fledge.example/enroll/done") {
		t.Errorf("redirect went to %q, want an absolute same-host completion page", location)
	}

	stored, err := server.store.Device(udid)
	if err != nil {
		t.Fatalf("the device was not recorded: %v", err)
	}
	if stored.Name != "Test iPhone" || stored.Product != "iPhone17,1" || stored.OSVersion != "26.0" {
		t.Errorf("stored device = %+v", stored)
	}

	done := get(t, server, "/enroll/done?c="+url.QueryEscape(challenge))
	if done.Code != http.StatusOK {
		t.Fatalf("completion page = %d", done.Code)
	}

	var bound bool
	for _, cookie := range done.Result().Cookies() {
		if cookie.Name == deviceCookie && strings.HasPrefix(cookie.Value, udid+".") {
			bound = true
		}
	}
	if !bound {
		t.Error("the completion page did not bind this browser to the device")
	}

	// A challenge is single use, so replaying it must not re-bind a browser.
	if replay := get(t, server, "/enroll/done?c="+url.QueryEscape(challenge)); replay.Code != http.StatusNotFound {
		t.Errorf("replaying a claimed challenge = %d, want 404", replay.Code)
	}
}

func TestCallbackRejectsAnUnknownChallenge(t *testing.T) {
	server := enrollServer(t)

	request := httptest.NewRequest(http.MethodPost, "/enroll/callback?c=deadbeef",
		strings.NewReader(string(deviceAnswer(t, "deadbeef", "00008140-AAAA"))))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Errorf("callback with an unissued challenge = %d, want 403", recorder.Code)
	}
}

func TestCallbackRejectsAMismatchedChallenge(t *testing.T) {
	server := enrollServer(t)

	profile := get(t, server, "/enroll/profile.mobileconfig?t="+enrollToken)
	challenge := challengeFromProfile(t, profile.Body.Bytes())

	// The device answers the right URL while claiming a different challenge,
	// which is what a replayed response body looks like.
	request := httptest.NewRequest(http.MethodPost, "/enroll/callback?c="+challenge,
		strings.NewReader(string(deviceAnswer(t, "some-other-challenge", "00008140-BBBB"))))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("mismatched challenge = %d, want 400", recorder.Code)
	}
}

func TestInstallPageWarnsWhenTheDeviceIsNotInTheProfile(t *testing.T) {
	server := newTestServer(t)
	buildID := upload(t, server)

	if err := server.store.PutDevice(&store.Device{UDID: "00008140-CCCC", Name: "Someone's iPad"}); err != nil {
		t.Fatalf("put device: %v", err)
	}

	recorder := httptest.NewRecorder()
	server.setDeviceCookie(recorder, "00008140-CCCC")

	request := httptest.NewRequest(http.MethodGet, "/a/dev.example.demo/"+buildID, nil)
	for _, cookie := range recorder.Result().Cookies() {
		request.AddCookie(cookie)
	}

	page := httptest.NewRecorder()
	server.ServeHTTP(page, request)

	// The synthetic archive has no profile at all, so the page must say it
	// cannot tell rather than claiming the install will work.
	if !strings.Contains(page.Body.String(), "No provisioning profile") {
		t.Error("the page does not flag the missing provisioning profile")
	}
}
