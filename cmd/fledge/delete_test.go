package main

import (
	"testing"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/ipa"
	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

func sample() []*store.Build {
	newest := time.Now()

	return []*store.Build{
		{ID: "aaa", App: &ipa.App{Version: "1.2", Build: "3"}, Uploaded: newest},
		{ID: "bbb", App: &ipa.App{Version: "1.1", Build: "2"}, Uploaded: newest.Add(-time.Hour)},
		{ID: "ccc", App: &ipa.App{Version: "1.0", Build: "1"}, Uploaded: newest.Add(-2 * time.Hour)},
	}
}

// TestSelectBuildsRefusesToGuess: the guess would be destructive.
func TestSelectBuildsRefusesToGuess(t *testing.T) {
	if _, err := selectBuilds(sample(), "", false, 0); err == nil {
		t.Error("naming nothing removed something")
	}
}

func TestSelectBuildsByIdentifierOrBuildNumber(t *testing.T) {
	chosen, err := selectBuilds(sample(), "bbb", false, 0)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if len(chosen) != 1 || chosen[0].ID != "bbb" {
		t.Errorf("by id chose %+v", chosen)
	}

	// A person reads "2" off `fledge builds` far more often than a digest.
	chosen, err = selectBuilds(sample(), "2", false, 0)
	if err != nil {
		t.Fatalf("by build number: %v", err)
	}
	if len(chosen) != 1 || chosen[0].ID != "bbb" {
		t.Errorf("by build number chose %+v", chosen)
	}

	if _, err := selectBuilds(sample(), "nope", false, 0); err == nil {
		t.Error("an unknown build selected something")
	}
}

func TestSelectBuildsAll(t *testing.T) {
	chosen, err := selectBuilds(sample(), "", true, 0)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(chosen) != 3 {
		t.Errorf("all chose %d builds, want 3", len(chosen))
	}
}

func TestSelectBuildsKeepsTheNewest(t *testing.T) {
	chosen, err := selectBuilds(sample(), "", false, 1)
	if err != nil {
		t.Fatalf("keep: %v", err)
	}
	if len(chosen) != 2 {
		t.Fatalf("keep 1 chose %d builds, want 2", len(chosen))
	}
	for _, build := range chosen {
		if build.ID == "aaa" {
			t.Error("keep 1 removed the newest build")
		}
	}

	if _, err := selectBuilds(sample(), "", false, 5); err == nil {
		t.Error("keeping more builds than exist reported work to do")
	}
}
