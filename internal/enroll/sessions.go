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
	invite string
}

// NewSessions builds an empty tracker.
func NewSessions() *Sessions {
	return &Sessions{pending: map[string]*session{}}
}

// Begin mints a challenge and starts tracking it.
func (s *Sessions) Begin() (string, error) {
	return s.BeginFor("")
}

// BeginFor mints a challenge and remembers which invitation produced it, so the
// right one is redeemed when a device answers.
func (s *Sessions) BeginFor(invite string) (string, error) {
	challenge, err := NewChallenge()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweep(time.Now())
	s.pending[challenge] = &session{issued: time.Now(), invite: invite}

	return challenge, nil
}

// Invite returns the invitation a challenge was issued against.
func (s *Sessions) Invite(challenge string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.pending[challenge]; ok {
		return entry.invite
	}

	return ""
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

// Claim returns the device that answered, without consuming the challenge: iOS
// follows the completion redirect itself and Safari then loads the same URL, so
// single use would show the person an empty page. Expiry bounds it instead.
func (s *Sessions) Claim(challenge string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[challenge]
	if !ok || entry.device == nil {
		return nil
	}

	return entry.device
}

// sweep drops challenges nobody is going to answer, and completed ones once the
// browser has had time to land on the result. Callers hold the lock.
func (s *Sessions) sweep(now time.Time) {
	for challenge, entry := range s.pending {
		if now.Sub(entry.issued) > time.Hour {
			delete(s.pending, challenge)
		}
	}
}

// Forget drops one challenge, so a finished enrolment can be closed out early.
func (s *Sessions) Forget(challenge string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pending, challenge)
}
