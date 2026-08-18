package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func buildCount(t *testing.T, server *Server) int {
	t.Helper()

	builds, err := server.store.Builds("dev.example.demo")
	if err != nil {
		t.Fatalf("read builds: %v", err)
	}

	return len(builds)
}

// TestUploadPrunesToTheConfiguredDepth keeps a PVC from filling with archives
// nobody will install.
func TestUploadPrunesToTheConfiguredDepth(t *testing.T) {
	server := newTestServer(t)
	server.cfg.KeepBuilds = 3

	for i := 1; i <= 6; i++ {
		uploadVersion(t, server, "1.0", string(rune('0'+i)), "release")
	}

	if got := buildCount(t, server); got != 3 {
		t.Errorf("kept %d builds, want 3", got)
	}

	// The survivors have to be the newest ones, or an install page would offer
	// something older than what was just published.
	builds, err := server.store.Builds("dev.example.demo")
	if err != nil {
		t.Fatalf("read builds: %v", err)
	}
	for _, build := range builds {
		if build.App.Build < "4" {
			t.Errorf("pruning kept an older build: %s", build.App.Build)
		}
	}
}

func TestZeroKeepsEverything(t *testing.T) {
	server := newTestServer(t)
	server.cfg.KeepBuilds = 0

	for i := 1; i <= 4; i++ {
		uploadVersion(t, server, "1.0", string(rune('0'+i)), "release")
	}

	if got := buildCount(t, server); got != 4 {
		t.Errorf("kept %d builds, want all 4", got)
	}
}

// TestUploadReportsWhatItPruned is how a release log says a build fell off the
// end, rather than it happening silently.
func TestUploadReportsWhatItPruned(t *testing.T) {
	server := newTestServer(t)
	server.cfg.KeepBuilds = 2

	for i := 1; i <= 2; i++ {
		uploadVersion(t, server, "1.0", string(rune('0'+i)), "release")
	}

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, uploadRequest(t, "1.0", "3", "release"))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload = %d", recorder.Code)
	}

	var response uploadResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Pruned != 1 {
		t.Errorf("pruned = %d, want 1", response.Pruned)
	}
}
