// Package oidc authorises CI to publish using a workload identity token rather
// than a shared secret.
package oidc

import (
	"fmt"
	"strings"
)

// Wildcard lets a repository publish any bundle identifier.
const Wildcard = "*"

// Rule grants one repository permission to publish some set of bundle
// identifiers, optionally only from one git ref.
type Rule struct {
	Repository string
	Ref        string
	Bundles    []string
}

// Policy is the set of repositories allowed to publish.
type Policy []Rule

// ParsePolicy reads the allowlist. The grammar is in the README under
// "Publishing from CI"; the tests are the other worked examples.
func ParsePolicy(raw string) (Policy, error) {
	var policy Policy

	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}

		subject, bundles, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("oidc: %q is not repository=bundles", entry)
		}

		rule := Rule{Repository: strings.TrimSpace(subject)}
		if repository, ref, hasRef := strings.Cut(rule.Repository, "@"); hasRef {
			rule.Repository, rule.Ref = strings.TrimSpace(repository), strings.TrimSpace(ref)
		}
		if !strings.Contains(rule.Repository, "/") {
			return nil, fmt.Errorf("oidc: %q is not an owner/repository", rule.Repository)
		}

		for _, bundle := range strings.Split(bundles, ",") {
			if bundle = strings.TrimSpace(bundle); bundle != "" {
				rule.Bundles = append(rule.Bundles, bundle)
			}
		}
		if len(rule.Bundles) == 0 {
			return nil, fmt.Errorf("oidc: %q grants no bundle identifiers", entry)
		}

		policy = append(policy, rule)
	}

	return policy, nil
}

// Identity is who a verified token says is calling.
type Identity struct {
	Repository string
	Ref        string
	Workflow   string
	Actor      string
	Subject    string
}

// String renders the identity for a log line.
func (i Identity) String() string {
	if i.Ref == "" {
		return i.Repository
	}
	return i.Repository + "@" + i.Ref
}

// Allows reports whether this identity may publish the bundle, and says why not
// when it may not, because a silent denial here looks like a broken token.
func (p Policy) Allows(identity Identity, bundleID string) error {
	var matchedRepository bool

	for _, rule := range p {
		if !strings.EqualFold(rule.Repository, identity.Repository) {
			continue
		}
		matchedRepository = true

		if rule.Ref != "" && rule.Ref != identity.Ref {
			continue
		}
		for _, bundle := range rule.Bundles {
			if bundle == Wildcard || bundle == bundleID {
				return nil
			}
		}
	}

	if !matchedRepository {
		return fmt.Errorf("%s is not allowed to publish to this server", identity.Repository)
	}

	return fmt.Errorf("%s is not allowed to publish %s", identity, bundleID)
}
