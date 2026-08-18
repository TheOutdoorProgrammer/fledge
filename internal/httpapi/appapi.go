package httpapi

import (
	"net/http"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

// entry is one release in a changelog.
type entry struct {
	Version   string    `json:"version"`
	Build     string    `json:"build"`
	BuildID   string    `json:"build_id"`
	Published time.Time `json:"published"`
	Notes     string    `json:"notes,omitempty"`
}

// latestResponse is what an app gets when it asks whether it is current.
type latestResponse struct {
	BundleID        string    `json:"bundle_id"`
	Name            string    `json:"name"`
	Version         string    `json:"version"`
	Build           string    `json:"build"`
	BuildID         string    `json:"build_id"`
	Published       time.Time `json:"published"`
	Size            int64     `json:"size"`
	MinimumOS       string    `json:"minimum_os,omitempty"`
	InstallPageURL  string    `json:"install_page_url"`
	UpdateAvailable bool      `json:"update_available"`
	Expired         bool      `json:"expired"`
	ExpiresAt       string    `json:"expires_at,omitempty"`
	Changelog       []entry   `json:"changelog"`
}

// handleLatest tells an app whether it is behind and what it missed. It is open
// because the app asking is installed already, and the answer is metadata the
// install page shows anyone anyway.
func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	bundleID := r.PathValue("bundle")

	latest, err := s.store.Latest(bundleID)
	if err != nil {
		s.failJSON(w, err)
		return
	}

	// The caller says which build it is running; anything else is a first run
	// or a build this server never published, and both want the full history.
	running := r.URL.Query().Get("build")
	newer, err := s.store.Since(bundleID, running)
	if err != nil {
		s.failJSON(w, err)
		return
	}

	response := latestResponse{
		BundleID:        latest.App.BundleID,
		Name:            latest.App.Name,
		Version:         latest.App.Version,
		Build:           latest.App.Build,
		BuildID:         latest.ID,
		Published:       latest.Uploaded,
		Size:            latest.App.Size,
		MinimumOS:       latest.App.MinimumOS,
		InstallPageURL:  s.URLFor("/a/" + latest.App.BundleID),
		UpdateAvailable: running != "" && running != latest.App.Build,
		Changelog:       changelog(newer),
	}

	if profile := latest.App.Profile; profile != nil {
		response.Expired = profile.Expired(time.Now())
		response.ExpiresAt = profile.Expires.Format(time.RFC3339)
	}

	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, response)
}

// changelog turns builds into releases, dropping the ones with nothing to say
// so an app does not render empty entries.
func changelog(builds []*store.Build) []entry {
	entries := make([]entry, 0, len(builds))
	for _, build := range builds {
		if build.Notes == "" {
			continue
		}
		entries = append(entries, entry{
			Version:   build.App.Version,
			Build:     build.App.Build,
			BuildID:   build.ID,
			Published: build.Uploaded,
			Notes:     build.Notes,
		})
	}

	return entries
}
