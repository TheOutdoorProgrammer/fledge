package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

// uploadResponse is what the CLI prints after a successful release.
type uploadResponse struct {
	BundleID   string `json:"bundle_id"`
	BuildID    string `json:"build_id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Build      string `json:"build"`
	Profile    string `json:"profile,omitempty"`
	Expires    string `json:"expires,omitempty"`
	Devices    int    `json:"devices"`
	InstallURL string `json:"install_url"`
	PageURL    string `json:"page_url"`
}

// handleUpload accepts an exported archive. The body is the archive itself
// rather than a multipart form, so the CLI can stream a large file without
// buffering it.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload)
	defer func() { _ = body.Close() }()

	build, err := s.store.Put(body, r.Header.Get("X-Fledge-Notes"))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(w, http.StatusRequestEntityTooLarge,
				"archive exceeds the configured limit of "+strconv.FormatInt(s.cfg.MaxUpload, 10)+" bytes")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	page := "/a/" + build.App.BundleID + "/" + build.ID
	response := uploadResponse{
		BundleID:   build.App.BundleID,
		BuildID:    build.ID,
		Name:       build.App.Name,
		Version:    build.App.Version,
		Build:      build.App.Build,
		Devices:    0,
		InstallURL: s.URLFor(page + "/manifest.plist"),
		PageURL:    s.URLFor(page),
	}
	if profile := build.App.Profile; profile != nil {
		response.Profile = string(profile.Type)
		response.Expires = profile.Expires.Format("2006-01-02")
		response.Devices = len(profile.Devices)
	}

	s.log.Info("published build",
		"bundle", build.App.BundleID, "version", build.App.Version,
		"build", build.App.Build, "id", build.ID)

	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handleListApps(w http.ResponseWriter, _ *http.Request) {
	bundles, err := s.store.Apps()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type entry struct {
		BundleID string       `json:"bundle_id"`
		Builds   int          `json:"builds"`
		Latest   *store.Build `json:"latest,omitempty"`
		PageURL  string       `json:"page_url"`
	}

	apps := make([]entry, 0, len(bundles))
	for _, bundle := range bundles {
		builds, err := s.store.Builds(bundle)
		if err != nil || len(builds) == 0 {
			continue
		}
		apps = append(apps, entry{
			BundleID: bundle,
			Builds:   len(builds),
			Latest:   builds[0],
			PageURL:  s.URLFor("/a/" + bundle),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func (s *Server) handleListBuilds(w http.ResponseWriter, r *http.Request) {
	builds, err := s.store.Builds(r.PathValue("bundle"))
	if err != nil {
		s.failJSON(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"builds": builds})
}

func (s *Server) handleDeleteBuild(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("bundle"), r.PathValue("build")); err != nil {
		s.failJSON(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	devices, err := s.store.Devices()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) failJSON(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"error": detail})
}
