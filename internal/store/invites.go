package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultInviteLifetime bounds an unused invite, because a link that never
// expires is a permanent way to spend an Apple device slot.
const DefaultInviteLifetime = 7 * 24 * time.Hour

// ErrInviteSpent is returned for an invite that is used, revoked or expired.
var ErrInviteSpent = errors.New("this invitation is no longer valid")

// Invite is a single permit to register one device.
type Invite struct {
	ID      string     `json:"id"`
	Note    string     `json:"note,omitempty"`
	Created time.Time  `json:"created"`
	Expires time.Time  `json:"expires"`
	Used    *time.Time `json:"used,omitempty"`
	UsedBy  string     `json:"used_by,omitempty"`
	Revoked *time.Time `json:"revoked,omitempty"`
}

// Spent reports whether the invite can still be redeemed.
func (i *Invite) Spent(now time.Time) bool {
	return i.Used != nil || i.Revoked != nil || now.After(i.Expires)
}

// State names what happened to it, for a list a person reads.
func (i *Invite) State(now time.Time) string {
	switch {
	case i.Revoked != nil:
		return "revoked"
	case i.Used != nil:
		return "used"
	case now.After(i.Expires):
		return "expired"
	default:
		return "open"
	}
}

// CreateInvite mints a permit. The identifier is the credential, so it carries
// 128 bits rather than being a readable name.
func (s *Store) CreateInvite(note string, lifetime time.Duration) (*Invite, error) {
	if lifetime <= 0 {
		lifetime = DefaultInviteLifetime
	}

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	invite := &Invite{
		ID:      hex.EncodeToString(raw),
		Note:    note,
		Created: now,
		Expires: now.Add(lifetime),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Join(s.root, "invites"), 0o750); err != nil {
		return nil, err
	}

	return invite, writeJSON(s.invitePath(invite.ID), invite)
}

// Invite reads one permit.
func (s *Store) Invite(id string) (*Invite, error) {
	if !isHex(id) {
		return nil, ErrNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.readInvite(id)
}

// RedeemInvite marks a permit used by a device, refusing to spend one twice.
func (s *Store) RedeemInvite(id, udid string) error {
	if !isHex(id) {
		return ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	invite, err := s.readInvite(id)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if invite.Spent(now) {
		return ErrInviteSpent
	}
	invite.Used, invite.UsedBy = &now, udid

	return writeJSON(s.invitePath(id), invite)
}

// RevokeInvite closes an unused permit.
func (s *Store) RevokeInvite(id string) error {
	if !isHex(id) {
		return ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	invite, err := s.readInvite(id)
	if err != nil {
		return err
	}
	if invite.Used != nil {
		return errors.New("store: that invitation has already been used")
	}

	now := time.Now().UTC()
	invite.Revoked = &now

	return writeJSON(s.invitePath(id), invite)
}

// Invites lists every permit, newest first.
func (s *Store) Invites() ([]*Invite, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(s.root, "invites"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	invites := make([]*Invite, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		invite, err := s.readInvite(entry.Name()[:len(entry.Name())-len(".json")])
		if err != nil {
			continue
		}
		invites = append(invites, invite)
	}
	sort.Slice(invites, func(i, j int) bool {
		return invites[i].Created.After(invites[j].Created)
	})

	return invites, nil
}

func (s *Store) readInvite(id string) (*Invite, error) {
	raw, err := os.ReadFile(s.invitePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var invite Invite
	if err := json.Unmarshal(raw, &invite); err != nil {
		return nil, err
	}

	return &invite, nil
}

func (s *Store) invitePath(id string) string {
	return filepath.Join(s.root, "invites", id+".json")
}
