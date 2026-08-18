// Package httpapi serves the install pages, the OTA manifests and the JSON API.
package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nerdswhofish/fledge/internal/config"
	"github.com/nerdswhofish/fledge/internal/store"
	"github.com/nerdswhofish/fledge/internal/web"
)

// deviceCookie remembers which device a browser belongs to, so an install page
// can say whether this phone is in the build's profile before it is tapped.
const deviceCookie = "fledge_device"

// Server wires the store and configuration to HTTP.
type Server struct {
	cfg       *config.Config
	store     *store.Store
	log       *slog.Logger
	mux       *http.ServeMux
	cookieKey []byte
}

// New builds a server ready to serve.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) *Server {
	key := sha256.Sum256([]byte("fledge-device-cookie\x00" + cfg.UploadToken))

	s := &Server{
		cfg:       cfg,
		store:     st,
		log:       log,
		mux:       http.NewServeMux(),
		cookieKey: key[:],
	}
	s.routes()

	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /assets/{asset}", s.handleWebAsset)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})

	s.mux.HandleFunc("GET /a/{bundle}", s.handleInstallPage)
	s.mux.HandleFunc("GET /a/{bundle}/{build}", s.handleInstallPage)
	s.mux.HandleFunc("GET /a/{bundle}/{build}/manifest.plist", s.handleManifest)
	s.mux.HandleFunc("GET /a/{bundle}/{build}/app.ipa", s.handlePackage)
	s.mux.HandleFunc("GET /a/{bundle}/{build}/icon.png", s.handleIcon)

	s.mux.Handle("POST /api/builds", s.authenticated(s.handleUpload))
	s.mux.Handle("GET /api/apps", s.authenticated(s.handleListApps))
	s.mux.Handle("GET /api/apps/{bundle}/builds", s.authenticated(s.handleListBuilds))
	s.mux.Handle("DELETE /api/apps/{bundle}/builds/{build}", s.authenticated(s.handleDeleteBuild))
	s.mux.Handle("GET /api/devices", s.authenticated(s.handleListDevices))
}

func (s *Server) handleWebAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	asset, err := web.Asset(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(asset))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// authenticated gates the write and inventory endpoints on the upload token.
// The install routes deliberately stay open: iOS fetches the manifest and the
// package from its own installer, which sends no credentials of any kind.
func (s *Server) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.UploadToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fledge"`)
			writeJSONError(w, http.StatusUnauthorized, "a valid bearer token is required")
			return
		}
		next(w, r)
	})
}

// URLFor builds an absolute URL from the configured public origin. Manifest and
// package URLs must be absolute and must match the origin iOS reached, which is
// why they are never derived from the request.
func (s *Server) URLFor(path string) string {
	return s.cfg.PublicURL + path
}

// setDeviceCookie ties this browser to an enrolled device.
func (s *Server) setDeviceCookie(w http.ResponseWriter, udid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookie,
		Value:    udid + "." + s.signCookie(udid),
		Path:     "/",
		MaxAge:   3600 * 24 * 365,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// deviceFromCookie returns the enrolled device this browser claims to be, or
// nil. A bad signature is treated as no cookie at all.
func (s *Server) deviceFromCookie(r *http.Request) *store.Device {
	cookie, err := r.Cookie(deviceCookie)
	if err != nil {
		return nil
	}

	udid, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(s.signCookie(udid))) {
		return nil
	}

	device, err := s.store.Device(udid)
	if err != nil {
		return nil
	}

	return device
}

func (s *Server) signCookie(udid string) string {
	mac := hmac.New(sha256.New, s.cookieKey)
	mac.Write([]byte(udid))
	return hex.EncodeToString(mac.Sum(nil))
}
