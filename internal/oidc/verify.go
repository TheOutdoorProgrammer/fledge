package oidc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// GitHubIssuer mints the workload identity tokens GitHub Actions requests.
const GitHubIssuer = "https://token.actions.githubusercontent.com"

// Verifier checks tokens against an issuer's published keys.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
	policy   Policy
}

// claims is the subset of GitHub's token Fledge authorises on.
type claims struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Workflow   string `json:"workflow"`
	Actor      string `json:"actor"`
}

// New builds a verifier. It reaches the issuer's discovery endpoint once, so a
// server configured for CI publishing will not start while that is unreachable.
func New(ctx context.Context, issuer, audience string, policy Policy) (*Verifier, error) {
	if audience == "" {
		return nil, errors.New("oidc: an audience is required, or any token for any service would be accepted")
	}
	if len(policy) == 0 {
		return nil, errors.New("oidc: a policy is required, or no repository could publish anything")
	}

	discovery, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	provider, err := oidc.NewProvider(discovery, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %s: %w", issuer, err)
	}

	return &Verifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
		policy:   policy,
	}, nil
}

// Verify checks a token's signature, issuer, audience and expiry, and returns
// who it says is calling. It does not decide what they may publish; that needs
// the bundle identifier, which is only known once the archive is parsed.
func (v *Verifier) Verify(ctx context.Context, raw string) (Identity, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc: %w", err)
	}

	var decoded claims
	if err := token.Claims(&decoded); err != nil {
		return Identity{}, fmt.Errorf("oidc: read claims: %w", err)
	}
	if decoded.Repository == "" {
		return Identity{}, errors.New("oidc: token carries no repository claim")
	}

	return Identity{
		Repository: decoded.Repository,
		Ref:        decoded.Ref,
		Workflow:   decoded.Workflow,
		Actor:      decoded.Actor,
		Subject:    token.Subject,
	}, nil
}

// Allows reports whether a verified identity may publish this bundle.
func (v *Verifier) Allows(identity Identity, bundleID string) error {
	return v.policy.Allows(identity, bundleID)
}
