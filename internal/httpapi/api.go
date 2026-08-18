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
	Pruned     int    `json:"pruned,omitempty"`
}

// prune trims an app's history to the configured depth. A failure here is
// logged rather than returned: the build was published, and reporting the
// upload as failed would invite a retry that publishes it twice.
func (s *Server) prune(bundleID string) int {
	if s.cfg.KeepBuilds <= 0 {
		return 0
	}

	removed, err := s.store.Prune(bundleID, s.cfg.KeepBuilds)
	if err != nil {
		s.log.Error("could not prune old builds", "bundle", bundleID, "error", err)
		return 0
	}
	if removed > 0 {
		s.log.Info("pruned old builds", "bundle", bundleID, "removed", removed, "kept", s.cfg.KeepBuilds)
	}

	return removed
}

// handleUpload accepts an exported archive. The body is the archive itself
// rather than a multipart form, so the CLI can stream a large file without
// buffering it.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload)
	defer func() { _ = body.Close() }()

	notes := r.URL.Query().Get("notes")
	if notes == "" {
		notes = r.Header.Get("X-Fledge-Notes")
	}

	build, err := s.store.Put(body, notes)
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

	// Authorisation waits until here because it is scoped to the bundle
	// identifier, which is only knowable once the archive has been read.
	if err := s.mayPublish(r, build.App.BundleID); err != nil {
		if removeErr := s.store.Delete(build.App.BundleID, build.ID); removeErr != nil {
			s.log.Error("could not discard an unauthorised upload", "error", removeErr)
		}
		s.log.Warn("refused a publish", "bundle", build.App.BundleID, "reason", err)
		writeJSONError(w, http.StatusForbidden, err.Error())
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
		"build", build.App.Build, "id", build.ID, "publisher", describePublisher(r))

	response.Pruned = s.prune(build.App.BundleID)

	writeJSON(w, http.StatusCreated, response)
}

// mayPublish reports whether the authenticated caller is allowed this bundle.
// The shared upload token is unrestricted; a workload identity is limited to
// what its policy names, so one repository cannot overwrite another's app.
func (s *Server) mayPublish(r *http.Request, bundleID string) error {
	who, ok := publisherFrom(r)
	if !ok {
		return errors.New("the request was not authenticated")
	}
	if !who.workload {
		return nil
	}

	return s.workloads.Allows(who.identity, bundleID)
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
