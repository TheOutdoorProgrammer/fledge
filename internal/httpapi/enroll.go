package httpapi

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/asc"
	"github.com/theoutdoorprogrammer/fledge/internal/enroll"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
	"github.com/theoutdoorprogrammer/fledge/internal/web"
)

// enrollCookie carries the enrolment token past the first navigation, so the
// link only has to be pasted once.
const enrollCookie = "fledge_enroll"

// maxProfileResponse bounds the device's callback body. The signed plist is a
// couple of kilobytes.
const maxProfileResponse = 128 << 10

type capacityView struct {
	Level    string
	Platform string
	Used     int
	Limit    int
}

type enrollView struct {
	Title    string
	Signed   bool
	Capacity *capacityView
}

func (s *Server) enrollRoutes() {
	s.mux.HandleFunc("GET /enroll", s.handleEnrollPage)
	s.mux.HandleFunc("GET /enroll/profile.mobileconfig", s.handleEnrollProfile)
	s.mux.HandleFunc("POST /enroll/callback", s.handleEnrollCallback)
	s.mux.HandleFunc("GET /enroll/done", s.handleEnrollDone)
	s.mux.HandleFunc("POST /enroll/rename", s.handleEnrollRename)
}

// authorizedToEnroll gates the pages a person drives. The callback and the
// completion page are not gated because iOS drives those and sends no token;
// they are bound by the challenge instead.
func (s *Server) authorizedToEnroll(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.Enroll.Enabled() {
		return false
	}

	presented := r.URL.Query().Get("t")
	if presented == "" {
		if cookie, err := r.Cookie(enrollCookie); err == nil {
			presented = cookie.Value
		}
	}
	if subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.Enroll.Token)) != 1 {
		return false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     enrollCookie,
		Value:    s.cfg.Enroll.Token,
		Path:     "/enroll",
		MaxAge:   3600,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	return true
}

func (s *Server) handleEnrollPage(w http.ResponseWriter, r *http.Request) {
	if !s.authorizedToEnroll(w, r) {
		s.enrollUnavailable(w)
		return
	}

	view := enrollView{Title: s.cfg.Title, Signed: s.cfg.Enroll.CanSign()}
	view.Capacity = s.appleCapacity(r)

	web.Render(w, http.StatusOK, "enroll", view)
}

// appleCapacity reports how much of the annual device allowance is spent. It is
// best effort: a slow or unconfigured Apple account must not stop an enrolment.
func (s *Server) appleCapacity(r *http.Request) *capacityView {
	if s.apple == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	counts, err := s.apple.Capacity(ctx)
	if err != nil {
		s.log.Warn("could not read Apple device capacity", "error", err)
		return nil
	}

	worst := &capacityView{Platform: "device", Limit: asc.DeviceLimit}
	for _, entry := range counts {
		if entry.Used > worst.Used {
			worst = &capacityView{Platform: entry.Platform, Used: entry.Used, Limit: entry.Limit}
		}
	}

	switch remaining := worst.Limit - worst.Used; {
	case remaining <= 0:
		worst.Level = "bad"
	case remaining <= 10:
		worst.Level = "warn"
	default:
		worst.Level = ""
	}

	return worst
}

func (s *Server) handleEnrollProfile(w http.ResponseWriter, r *http.Request) {
	if !s.authorizedToEnroll(w, r) {
		s.enrollUnavailable(w)
		return
	}

	challenge, err := s.sessions.Begin()
	if err != nil {
		s.fail(w, r, err)
		return
	}

	document, err := enroll.Profile(enroll.Options{
		CallbackURL:  s.URLFor("/enroll/callback?c=" + challenge),
		Challenge:    challenge,
		Organization: s.cfg.Title,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if s.cfg.Enroll.CanSign() {
		signed, err := enroll.Sign(document, s.cfg.Enroll.SigningCert, s.cfg.Enroll.SigningKey)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		document = signed
	}

	w.Header().Set("Content-Type", enroll.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="fledge-enroll.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(document)
}

// handleEnrollCallback receives the device's answer. Responding with a redirect
// is what makes iOS reopen Safari on the completion page.
func (s *Server) handleEnrollCallback(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("c")
	if !s.sessions.Outstanding(challenge) {
		http.Error(w, "unknown or expired enrolment", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxProfileResponse))
	if err != nil {
		http.Error(w, "could not read the response", http.StatusBadRequest)
		return
	}

	device, err := enroll.ParseResponse(body, challenge)
	if err != nil {
		s.log.Warn("rejected an enrolment response", "error", err)
		http.Error(w, "the device response could not be verified", http.StatusBadRequest)
		return
	}

	record := &store.Device{
		UDID:      device.UDID,
		Name:      device.DisplayName(),
		Product:   device.Product,
		OSVersion: device.Version,
		Serial:    device.Serial,
		Enrolled:  time.Now().UTC(),
	}
	if err := s.store.PutDevice(record); err != nil {
		s.log.Error("could not record an enrolled device", "error", err)
		http.Error(w, "could not record the device", http.StatusInternalServerError)
		return
	}

	s.sessions.Complete(challenge, device)
	s.log.Info("device enrolled", "udid", device.UDID, "name", record.Name, "product", device.Product)

	http.Redirect(w, r, s.URLFor("/enroll/done?c="+challenge), http.StatusFound)
}

func (s *Server) handleEnrollDone(w http.ResponseWriter, r *http.Request) {
	device := s.sessions.Claim(r.URL.Query().Get("c"))
	if device == nil {
		web.Render(w, http.StatusNotFound, "message", map[string]any{
			"Title":     s.cfg.Title,
			"Heading":   "Nothing to finish",
			"Detail":    "This registration link has already been used, or it expired. Start again from the registration page.",
			"BackLink":  "/enroll",
			"BackLabel": "Start again",
		})
		return
	}

	s.setDeviceCookie(w, device.UDID)

	notices := []Notice{{
		Level: "ok",
		Title: device.DisplayName() + " is registered with Fledge",
		Body:  "Install pages will now tell you in advance whether a build is signed for this device.",
	}}
	if s.apple == nil {
		notices = append(notices, Notice{
			Level: "warn",
			Title: "Not yet registered with Apple",
			Body:  "Fledge has no App Store Connect credentials, so add this UDID to your team by hand, then publish a new build.",
		})
	} else {
		notices = append(notices, s.registerWithApple(r, device))
	}

	web.Render(w, http.StatusOK, "message", map[string]any{
		"Title":     s.cfg.Title,
		"Heading":   "Registered",
		"Detail":    device.UDID,
		"Notices":   notices,
		"BackLink":  "/",
		"BackLabel": "See published apps",
	})
}

// registerWithApple adds the device to the developer team. A slot spent here is
// never returned, so the outcome is reported rather than passed over quietly.
func (s *Server) registerWithApple(r *http.Request, device *enroll.Device) Notice {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	registered, consumed, err := s.apple.RegisterDevice(ctx,
		device.DisplayName(), device.UDID, enroll.Platform(device.Product))
	if err != nil {
		s.log.Error("apple device registration failed", "udid", device.UDID, "error", err)
		return Notice{
			Level: "bad",
			Title: "Apple would not register this device",
			Body:  err.Error(),
		}
	}

	if err := s.markRegistered(device.UDID, registered.ID, registered.Name); err != nil {
		s.log.Warn("could not record the Apple device id", "error", err)
	}

	if registered.Name != "" && registered.Name != device.DisplayName() {
		return Notice{
			Level: "warn",
			Title: "Apple calls this device something else",
			Body: "Your developer account has it as " + registered.Name + ", while the device reports " +
				device.DisplayName() + ". Nothing is broken, but the name in the portal is misleading.",
			Action:      "/enroll/rename",
			ActionLabel: "Rename it to " + device.DisplayName(),
		}
	}

	if consumed {
		s.log.Info("consumed an Apple device slot", "udid", device.UDID)
		return Notice{
			Level: "warn",
			Title: "Registered with Apple, one slot spent",
			Body:  "Apple does not return this slot if the device is removed. Publish a new build so its profile includes this device.",
		}
	}

	return Notice{
		Level: "ok",
		Title: "Already registered with Apple",
		Body:  "No device slot was used. Publish a new build if this device is not yet in the profile.",
	}
}

func (s *Server) markRegistered(udid, appleID, appleName string) error {
	device, err := s.store.Device(udid)
	if err != nil {
		return err
	}
	device.Registered, device.AppleID, device.AppleName = true, appleID, appleName

	return s.store.PutDevice(device)
}

// handleEnrollRename applies the rename the completion page offered. It is
// authorised by the device cookie, so a browser can only rename the device it
// enrolled, and it happens only because someone pressed the button.
func (s *Server) handleEnrollRename(w http.ResponseWriter, r *http.Request) {
	device := s.deviceFromCookie(r)
	if device == nil || device.AppleID == "" || s.apple == nil {
		s.enrollUnavailable(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	renamed, err := s.apple.RenameDevice(ctx, device.AppleID, device.Name)
	if err != nil {
		s.log.Error("apple rename failed", "udid", device.UDID, "error", err)
		web.Render(w, http.StatusBadGateway, "message", map[string]any{
			"Title":     s.cfg.Title,
			"Heading":   "Apple would not rename it",
			"Detail":    err.Error(),
			"BackLink":  "/",
			"BackLabel": "See published apps",
		})
		return
	}

	device.AppleName = renamed.Name
	if err := s.store.PutDevice(device); err != nil {
		s.log.Warn("could not record the new Apple name", "error", err)
	}
	s.log.Info("renamed an Apple device", "udid", device.UDID, "name", renamed.Name)

	web.Render(w, http.StatusOK, "message", map[string]any{
		"Title":     s.cfg.Title,
		"Heading":   "Renamed",
		"Detail":    "Your developer account now calls this device " + renamed.Name + ".",
		"BackLink":  "/",
		"BackLabel": "See published apps",
	})
}

func (s *Server) enrollUnavailable(w http.ResponseWriter) {
	heading, detail := "Registration is closed", "Fledge was started without an enrolment token, so it cannot register devices."
	if s.cfg.Enroll.Enabled() {
		heading, detail = "Not this link", "That registration link is not valid. Ask for a current one."
	}

	web.Render(w, http.StatusNotFound, "message", map[string]any{
		"Title":     s.cfg.Title,
		"Heading":   heading,
		"Detail":    detail,
		"BackLink":  "/",
		"BackLabel": "See published apps",
	})
}
