package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/theoutdoorprogrammer/fledge/internal/manifest"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
	"github.com/theoutdoorprogrammer/fledge/internal/web"
)

// expiryWarning is how close a provisioning profile has to be to lapsing before
// the install page starts nagging about it.
const expiryWarning = 14 * 24 * time.Hour

// Notice is a banner on an install or enrolment page. An Action turns it into a
// form, which is how a change Fledge will not make on its own gets offered.
type Notice struct {
	Level       string
	Title       string
	Body        string
	Action      string
	ActionLabel string
}

type installView struct {
	Title        string
	Build        *store.Build
	History      []*store.Build
	IsLatest     bool
	Installable  bool
	Expired      bool
	ExpiringSoon bool
	InstallURL   template.URL
	IconURL      string
	BasePath     string
	QRDataURI    template.URL
	Notices      []Notice
}

type appSummary struct {
	BundleID     string
	Name         string
	Version      string
	Build        string
	Uploaded     time.Time
	ProfileType  string
	Expired      bool
	ExpiringSoon bool
	BuildCount   int
	IconURL      string
}

type indexView struct {
	Title  string
	Apps   []appSummary
	Device *store.Device
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	bundles, err := s.store.Apps()
	if err != nil {
		s.fail(w, r, err)
		return
	}

	now := time.Now()
	view := indexView{Title: s.cfg.Title, Device: s.deviceFromCookie(r)}
	for _, bundle := range bundles {
		builds, err := s.store.Builds(bundle)
		if err != nil || len(builds) == 0 {
			continue
		}
		latest := builds[0]
		summary := appSummary{
			BundleID:   bundle,
			Name:       latest.App.Name,
			Version:    latest.App.Version,
			Build:      latest.App.Build,
			Uploaded:   latest.Uploaded,
			BuildCount: len(builds),
			IconURL:    "/a/" + bundle + "/" + latest.ID + "/icon.png",
		}
		if profile := latest.App.Profile; profile != nil {
			summary.ProfileType = string(profile.Type)
			summary.Expired = profile.Expired(now)
			summary.ExpiringSoon = profile.ExpiresWithin(now, expiryWarning)
		}
		view.Apps = append(view.Apps, summary)
	}

	web.Render(w, http.StatusOK, "index", view)
}

func (s *Server) handleInstallPage(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue("bundle")

	history, err := s.store.Builds(bundle)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	build, err := s.resolveBuild(bundle, r.PathValue("build"), history)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	base := "/a/" + bundle
	view := installView{
		Title:      s.cfg.Title,
		Build:      build,
		History:    history,
		IsLatest:   len(history) > 0 && history[0].ID == build.ID,
		BasePath:   base,
		IconURL:    base + "/" + build.ID + "/icon.png",
		InstallURL: template.URL(manifest.InstallURL(s.URLFor(base + "/" + build.ID + "/manifest.plist"))),
	}
	view.QRDataURI = template.URL(qrDataURI(s.URLFor(base + "/" + build.ID)))
	view.Notices, view.Installable, view.Expired, view.ExpiringSoon = s.assess(build, r)

	web.Render(w, http.StatusOK, "install", view)
}

// assess produces the banners above the install button, and decides whether the
// button should work at all.
func (s *Server) assess(build *store.Build, r *http.Request) (notices []Notice, installable, expired, expiringSoon bool) {
	now := time.Now()
	installable = true

	if inAppBrowser(r.UserAgent()) {
		installable = false
		notices = append(notices, Notice{
			Level: "bad",
			Title: "Open this page in Safari",
			Body:  "App browsers silently drop the install, with no error. Tap the share icon and choose Open in Safari.",
		})
	}

	profile := build.App.Profile
	if profile == nil {
		return append(notices, Notice{
			Level: "warn",
			Title: "No provisioning profile",
			Body:  "This archive has no embedded profile, so Fledge cannot tell whether it will install.",
		}), installable, false, false
	}

	expired = profile.Expired(now)
	expiringSoon = profile.ExpiresWithin(now, expiryWarning)

	switch {
	case !profile.Type.InstallsOverTheAir():
		installable = false
		notices = append(notices, Notice{
			Level: "bad",
			Title: "App Store build",
			Body:  "This archive is signed for App Store submission and cannot be installed over the air.",
		})
	case expired:
		installable = false
		notices = append(notices, Notice{
			Level: "bad",
			Title: "This build has expired",
			Body:  "Its provisioning profile has lapsed, so iOS will refuse to launch it. Publish a fresh build.",
		})
	case expiringSoon:
		notices = append(notices, Notice{
			Level: "warn",
			Title: "Expiring soon",
			Body:  "Once the profile lapses the installed app stops launching, even though it stays on the Home Screen.",
		})
	}

	notices = append(notices, s.deviceNotice(build, r)...)

	if build.App.RequiresDeveloperMode() && installable {
		notices = append(notices, Notice{
			Level: "warn",
			Title: "Developer Mode is required",
			Body:  "This build is signed with a developer account, so iOS will not launch it until Developer Mode is on. You will need to restart the device.",
		})
	}

	return notices, installable, expired, expiringSoon
}

// deviceNotice reports whether the visiting device is in this build's profile,
// which is the failure iOS otherwise reports as an unexplained "Unable to
// Install".
func (s *Server) deviceNotice(build *store.Build, r *http.Request) []Notice {
	device := s.deviceFromCookie(r)
	if device == nil {
		return []Notice{{
			Level: "",
			Title: "Fledge does not know this device",
			Body:  "Register it and Fledge can tell you in advance whether a build will install, instead of iOS failing without saying why.",
		}}
	}

	if build.App.Profile.Authorizes(device.UDID) {
		return []Notice{{
			Level: "ok",
			Title: "This build is signed for " + device.Name,
			Body:  "The device is named in the provisioning profile, so the install will go through.",
		}}
	}

	return []Notice{{
		Level: "bad",
		Title: device.Name + " is not in this build",
		Body:  "iOS will refuse the install without explaining why. Register the device with Apple, then publish a new build so the profile includes it.",
	}}
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	bundle, buildID := r.PathValue("bundle"), r.PathValue("build")

	build, err := s.store.Build(bundle, buildID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	base := s.URLFor("/a/" + bundle + "/" + buildID)
	icon := base + "/icon.png"
	document, err := manifest.Manifest(manifest.Build{
		BundleIdentifier:   build.App.BundleID,
		BundleVersion:      build.App.Version,
		Title:              build.App.Name,
		Subtitle:           build.Notes,
		PlatformIdentifier: manifest.PlatformFor(build.App.Platforms),
		PackageURL:         base + "/app.ipa",
		DisplayImageURL:    icon,
		FullSizeImageURL:   icon,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// Apple's deployment guide specifies text/xml here, and iOS is fussy enough
	// about the manifest that it is not worth deviating.
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(document)
}

func (s *Server) handlePackage(w http.ResponseWriter, r *http.Request) {
	file, _, err := s.store.OpenPackage(r.PathValue("bundle"), r.PathValue("build"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "app.ipa", time.Time{}, file)
}

func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	icon, err := s.store.Icon(r.PathValue("bundle"), r.PathValue("build"))
	if err != nil {
		icon = web.PlaceholderIcon()
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "icon.png", time.Time{}, bytes.NewReader(icon))
}

// resolveBuild picks the requested build, or the newest when none was named.
func (s *Server) resolveBuild(bundle, buildID string, history []*store.Build) (*store.Build, error) {
	if buildID != "" {
		return s.store.Build(bundle, buildID)
	}
	if len(history) == 0 {
		return nil, store.ErrNotFound
	}
	return history[0], nil
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		web.Render(w, http.StatusNotFound, "message", map[string]any{
			"Title":     s.cfg.Title,
			"Heading":   "Not here",
			"Detail":    "That app or build is not published.",
			"BackLink":  "/",
			"BackLabel": "See what is",
		})
		return
	}

	s.log.Error("request failed", "path", r.URL.Path, "error", err)
	web.Render(w, http.StatusInternalServerError, "message", map[string]any{
		"Title":   s.cfg.Title,
		"Heading": "Something broke",
		"Detail":  "The server logged the details.",
	})
}

// inAppBrowser spots the embedded webviews that swallow itms-services links
// without reporting an error.
func inAppBrowser(userAgent string) bool {
	for _, marker := range []string{"FBAN", "FBAV", "Instagram", "Line/", "MicroMessenger", "Twitter", "Slack", "LinkedInApp"} {
		if strings.Contains(userAgent, marker) {
			return true
		}
	}
	return false
}

func qrDataURI(target string) string {
	code, err := qr.Encode(target, qr.M)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
}
