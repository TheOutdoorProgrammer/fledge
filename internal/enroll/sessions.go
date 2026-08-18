package enroll

import (
	"sync"
	"time"
)

// Sessions tracks in-flight enrolments. Memory is enough: a challenge is
// worthless after ten minutes, so losing them on restart costs one extra tap.
type Sessions struct {
	mu      sync.Mutex
	pending map[string]*session
}

type session struct {
	issued time.Time
	device *Device
}

// NewSessions builds an empty tracker.
func NewSessions() *Sessions {
	return &Sessions{pending: map[string]*session{}}
}

// Begin mints a challenge and starts tracking it.
func (s *Sessions) Begin() (string, error) {
	challenge, err := NewChallenge()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweep(time.Now())
	s.pending[challenge] = &session{issued: time.Now()}

	return challenge, nil
}

// Outstanding reports whether a challenge is live and still unanswered.
func (s *Sessions) Outstanding(challenge string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[challenge]

	return ok && entry.device == nil && !Request{Issued: entry.issued}.Expired(time.Now())
}

// Complete attaches the device that answered a challenge.
func (s *Sessions) Complete(challenge string, device *Device) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[challenge]
	if !ok {
		return false
	}
	entry.device = device

	return true
}

// Claim hands back the device that answered a challenge and forgets it, so the
// same challenge cannot be redeemed for a second browser session.
func (s *Sessions) Claim(challenge string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[challenge]
	if !ok || entry.device == nil {
		return nil
	}
	delete(s.pending, challenge)

	return entry.device
}

// sweep drops challenges nobody is going to answer. Callers hold the lock.
func (s *Sessions) sweep(now time.Time) {
	for challenge, entry := range s.pending {
		if now.Sub(entry.issued) > time.Hour {
			delete(s.pending, challenge)
		}
	}
}
