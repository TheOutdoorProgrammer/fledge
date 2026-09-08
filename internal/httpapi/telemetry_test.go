package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theoutdoorprogrammer/fledge/internal/config"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
	"github.com/theoutdoorprogrammer/fledge/internal/telemetry"
)

func TestCorruptBuildFailsVisiblyWithoutPrivateDetails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	buildDir := filepath.Join(dir, "apps", "dev.example.demo", "builds", "012345abcdef")
	if err := os.MkdirAll(buildDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "build.json"), []byte("private invalid metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(telemetry.Logger(&logs))
	defer slog.SetDefault(previous)
	server := New(&config.Config{UploadToken: testToken}, st, Options{}, slog.Default())
	request := httptest.NewRequest("GET", "/api/apps", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	response := httptest.NewRecorder()
	telemetry.HTTPHandler(server).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatal("failure was not logged")
	}
	if strings.Contains(logs.String()+response.Body.String(), "private") || strings.Contains(logs.String(), dir) {
		t.Fatal("private failure details escaped")
	}
}
