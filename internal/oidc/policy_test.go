package oidc

import (
	"strings"
	"testing"
)

func TestParsePolicyReadsTheDocumentedGrammar(t *testing.T) {
	policy, err := ParsePolicy(`
		# one repository, one app
		owner/app=com.example.app
		owner/monorepo@refs/heads/main=com.example.one, com.example.two
		owner/trusted=*
	`)
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if len(policy) != 3 {
		t.Fatalf("parsed %d rules, want 3", len(policy))
	}

	if policy[1].Ref != "refs/heads/main" {
		t.Errorf("ref = %q", policy[1].Ref)
	}
	if len(policy[1].Bundles) != 2 {
		t.Errorf("bundles = %v, want two", policy[1].Bundles)
	}
	if policy[2].Bundles[0] != Wildcard {
		t.Errorf("wildcard rule = %v", policy[2].Bundles)
	}
}

func TestParsePolicyRejectsNonsense(t *testing.T) {
	for _, raw := range []string{
		"owner/app",
		"notarepo=com.example.app",
		"owner/app=",
	} {
		if _, err := ParsePolicy(raw); err == nil {
			t.Errorf("ParsePolicy(%q) accepted an unusable rule", raw)
		}
	}
}

// TestPolicyKeepsRepositoriesOutOfEachOthersApps is the property that makes
// workload identity worth having over a shared token.
func TestPolicyKeepsRepositoriesOutOfEachOthersApps(t *testing.T) {
	policy, err := ParsePolicy("owner/one=com.example.one\nowner/two=com.example.two")
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}

	one := Identity{Repository: "owner/one", Ref: "refs/heads/main"}
	if err := policy.Allows(one, "com.example.one"); err != nil {
		t.Errorf("owner/one may not publish its own app: %v", err)
	}
	if err := policy.Allows(one, "com.example.two"); err == nil {
		t.Error("owner/one was allowed to publish owner/two's app")
	}

	stranger := Identity{Repository: "someone/else"}
	if err := policy.Allows(stranger, "com.example.one"); err == nil {
		t.Error("an unlisted repository was allowed to publish")
	} else if !strings.Contains(err.Error(), "not allowed to publish to this server") {
		t.Errorf("unhelpful denial for an unlisted repository: %v", err)
	}
}

func TestPolicyHonoursARefConstraint(t *testing.T) {
	policy, err := ParsePolicy("owner/app@refs/heads/main=com.example.app")
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}

	main := Identity{Repository: "owner/app", Ref: "refs/heads/main"}
	if err := policy.Allows(main, "com.example.app"); err != nil {
		t.Errorf("main was refused: %v", err)
	}

	branch := Identity{Repository: "owner/app", Ref: "refs/heads/someones-fork"}
	if err := policy.Allows(branch, "com.example.app"); err == nil {
		t.Error("a branch outside the ref constraint was allowed to publish")
	}

	// A pull request from a fork runs with a ref the policy does not name, which
	// is exactly the case the constraint exists to stop.
	pull := Identity{Repository: "owner/app", Ref: "refs/pull/42/merge"}
	if err := policy.Allows(pull, "com.example.app"); err == nil {
		t.Error("a pull request ref was allowed to publish")
	}
}

func TestWildcardGrantsEveryBundle(t *testing.T) {
	policy, err := ParsePolicy("owner/trusted=*")
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}

	trusted := Identity{Repository: "owner/trusted"}
	for _, bundle := range []string{"com.example.one", "com.example.two"} {
		if err := policy.Allows(trusted, bundle); err != nil {
			t.Errorf("wildcard refused %s: %v", bundle, err)
		}
	}
}

func TestRepositoryMatchIgnoresCase(t *testing.T) {
	policy, err := ParsePolicy("TheOutdoorProgrammer/App=com.example.app")
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}

	if err := policy.Allows(Identity{Repository: "theoutdoorprogrammer/app"}, "com.example.app"); err != nil {
		t.Errorf("a differently cased repository was refused: %v", err)
	}
}
