package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

// inviteView is an invitation as the CLI and the admin see it.
type inviteView struct {
	ID      string     `json:"id"`
	Note    string     `json:"note,omitempty"`
	State   string     `json:"state"`
	URL     string     `json:"url,omitempty"`
	Created time.Time  `json:"created"`
	Expires time.Time  `json:"expires"`
	Used    *time.Time `json:"used,omitempty"`
	UsedBy  string     `json:"used_by,omitempty"`
}

// present renders an invitation, withholding the link once it can no longer be
// redeemed so a spent one cannot be handed out by mistake.
func (s *Server) present(invite *store.Invite) inviteView {
	view := inviteView{
		ID:      invite.ID,
		Note:    invite.Note,
		State:   invite.State(time.Now()),
		Created: invite.Created,
		Expires: invite.Expires,
		Used:    invite.Used,
		UsedBy:  invite.UsedBy,
	}
	if view.State == "open" {
		view.URL = s.URLFor("/enroll?t=" + invite.ID)
	}

	return view
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Note    string `json:"note"`
		Expires string `json:"expires"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, "could not read the request: "+err.Error())
			return
		}
	}

	lifetime := store.DefaultInviteLifetime
	if request.Expires != "" {
		parsed, err := time.ParseDuration(request.Expires)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, "expires must be a positive duration such as 168h")
			return
		}
		lifetime = parsed
	}

	invite, err := s.store.CreateInvite(request.Note, lifetime)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.log.Info("created an invitation", "id", invite.ID, "note", invite.Note, "expires", invite.Expires)
	writeJSON(w, http.StatusCreated, s.present(invite))
}

func (s *Server) handleListInvites(w http.ResponseWriter, _ *http.Request) {
	invites, err := s.store.Invites()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	views := make([]inviteView, 0, len(invites))
	for _, invite := range invites {
		views = append(views, s.present(invite))
	}

	writeJSON(w, http.StatusOK, map[string]any{"invites": views})
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeInvite(r.PathValue("invite")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "no such invitation")
			return
		}
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}

	s.log.Info("revoked an invitation", "id", r.PathValue("invite"))
	w.WriteHeader(http.StatusNoContent)
}
