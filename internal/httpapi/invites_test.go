package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func createInvite(t *testing.T, server *Server, body string) inviteView {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/api/invites", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("create invite = %d: %s", recorder.Code, recorder.Body)
	}

	var invite inviteView
	if err := json.Unmarshal(recorder.Body.Bytes(), &invite); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return invite
}

func TestInvitesNeedTheAdminToken(t *testing.T) {
	server := newTestServer(t)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/invites", strings.NewReader("{}")),
		httptest.NewRequest(http.MethodGet, "/api/invites", nil),
		httptest.NewRequest(http.MethodDelete, "/api/invites/abc", nil),
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestAnInvitationOpensEnrolment(t *testing.T) {
	server := newTestServer(t)
	invite := createInvite(t, server, `{"note":"Kay's iPhone"}`)

	if invite.State != "open" || invite.URL == "" {
		t.Fatalf("new invite = %+v", invite)
	}
	if !strings.Contains(invite.URL, "/enroll?t="+invite.ID) {
		t.Errorf("invite URL = %q", invite.URL)
	}

	page := get(t, server, "/enroll?t="+invite.ID)
	if page.Code != http.StatusOK {
		t.Fatalf("enrolment with a fresh invite = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "Kay&#39;s iPhone") {
		t.Error("the page does not say who the invitation is for")
	}

	if unknown := get(t, server, "/enroll?t=0123456789abcdef"); unknown.Code != http.StatusNotFound {
		t.Errorf("enrolment with an unissued invite = %d, want 404", unknown.Code)
	}
}

// TestAnInvitationIsSpentByTheDevice pins the property that matters: a permit
// is consumed by a registration, not by opening the page.
func TestAnInvitationIsSpentByTheDevice(t *testing.T) {
	server := newTestServer(t)
	server.cfg.Enroll.Token = ""
	invite := createInvite(t, server, `{"note":"one device"}`)

	profile := get(t, server, "/enroll/profile.mobileconfig?t="+invite.ID)
	if profile.Code != http.StatusOK {
		t.Fatalf("profile download = %d", profile.Code)
	}

	// Downloading the profile must not spend it; people open pages and wander off.
	stored, err := server.store.Invite(invite.ID)
	if err != nil {
		t.Fatalf("read invite: %v", err)
	}
	if stored.Spent(time.Now()) {
		t.Fatal("the invitation was spent merely by downloading the profile")
	}

	challenge := challengeFromProfile(t, profile.Body.Bytes())
	callback := httptest.NewRequest(http.MethodPost,
		"/enroll/callback?c="+url.QueryEscape(challenge),
		strings.NewReader(string(deviceAnswer(t, challenge, "00008140-FFFF"))))
	answered := httptest.NewRecorder()
	server.ServeHTTP(answered, callback)

	if answered.Code != http.StatusMovedPermanently {
		t.Fatalf("callback = %d: %s", answered.Code, answered.Body)
	}

	stored, err = server.store.Invite(invite.ID)
	if err != nil {
		t.Fatalf("read invite: %v", err)
	}
	if stored.Used == nil {
		t.Fatal("registering a device did not spend the invitation")
	}
	if stored.UsedBy != "00008140-FFFF" {
		t.Errorf("UsedBy = %q", stored.UsedBy)
	}

	// Spent means spent: the same link must not open enrolment again.
	if again := get(t, server, "/enroll?t="+invite.ID); again.Code != http.StatusNotFound {
		t.Errorf("a spent invitation still opened enrolment: %d", again.Code)
	}
}

func TestRevokingClosesAnInvitation(t *testing.T) {
	server := newTestServer(t)
	server.cfg.Enroll.Token = ""
	invite := createInvite(t, server, `{"note":"changed my mind"}`)

	request := httptest.NewRequest(http.MethodDelete, "/api/invites/"+invite.ID, nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", recorder.Code, recorder.Body)
	}
	if page := get(t, server, "/enroll?t="+invite.ID); page.Code != http.StatusNotFound {
		t.Errorf("a revoked invitation still opened enrolment: %d", page.Code)
	}
}

func TestAnExpiredInvitationIsRefused(t *testing.T) {
	server := newTestServer(t)
	server.cfg.Enroll.Token = ""

	invite, err := server.store.CreateInvite("stale", time.Millisecond)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if page := get(t, server, "/enroll?t="+invite.ID); page.Code != http.StatusNotFound {
		t.Errorf("an expired invitation opened enrolment: %d", page.Code)
	}
}

func TestListedInvitesHideTheLinkOnceSpent(t *testing.T) {
	server := newTestServer(t)
	invite := createInvite(t, server, `{"note":"revoke me"}`)
	if err := server.store.RevokeInvite(invite.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/invites", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	var response struct {
		Invites []inviteView `json:"invites"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Invites) != 1 {
		t.Fatalf("listed %d invites", len(response.Invites))
	}
	if response.Invites[0].State != "revoked" {
		t.Errorf("state = %q", response.Invites[0].State)
	}
	if response.Invites[0].URL != "" {
		t.Error("a revoked invitation still advertises its link")
	}
}
