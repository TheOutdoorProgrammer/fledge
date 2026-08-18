package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/theoutdoorprogrammer/fledge/internal/store"
)

func deleteCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("delete", flag.ExitOnError)
	all := flags.Bool("all", false, "remove every build of the app, not just one")
	keep := flags.Int("keep", 0, "remove all but this many of the newest builds")
	yes := flags.Bool("y", false, "do not ask")
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() < 1 {
		return errors.New("usage: fledge delete [-all|-keep N] <bundle-id> [build-id]")
	}

	// Go's flag package stops at the first non-flag argument, so a flag written
	// after the bundle identifier silently becomes a build id and would delete
	// the wrong thing. Say so rather than act on it.
	for _, arg := range flags.Args()[1:] {
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("put %s before the bundle identifier: fledge delete %s %s", arg, arg, flags.Arg(0))
		}
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	bundleID := flags.Arg(0)
	builds, err := api.Builds(ctx, bundleID)
	if err != nil {
		return err
	}
	if len(builds) == 0 {
		return fmt.Errorf("%s has no builds", bundleID)
	}

	doomed, err := selectBuilds(builds, flags.Arg(1), *all, *keep)
	if err != nil {
		return err
	}

	fmt.Printf("About to remove %d build(s) of %s:\n", len(doomed), bundleID)
	for _, build := range doomed {
		fmt.Printf("  %s  %s (%s)  %s\n", build.ID, build.App.Version, build.App.Build,
			build.Uploaded.Format("2006-01-02 15:04"))
	}
	if len(doomed) == len(builds) {
		fmt.Println("\nThat is every build, so the app disappears from the index.")
	}

	if !*yes && !confirm() {
		fmt.Println("left alone")
		return nil
	}

	for _, build := range doomed {
		if err := api.Delete(ctx, bundleID, build.ID); err != nil {
			return fmt.Errorf("removing %s: %w", build.ID, err)
		}
		fmt.Printf("removed %s\n", build.ID)
	}

	return nil
}

// selectBuilds decides what to remove. Naming nothing is an error rather than a
// guess, because the guess would be destructive.
func selectBuilds(builds []*store.Build, buildID string, all bool, keep int) ([]*store.Build, error) {
	switch {
	case buildID != "":
		for _, build := range builds {
			if build.ID == buildID || build.App.Build == buildID {
				return []*store.Build{build}, nil
			}
		}
		return nil, fmt.Errorf("no build %q", buildID)

	case all:
		return builds, nil

	case keep > 0:
		if len(builds) <= keep {
			return nil, fmt.Errorf("only %d build(s) published, nothing to remove", len(builds))
		}
		return builds[keep:], nil

	default:
		return nil, errors.New("say which: a build id, -all, or -keep N")
	}
}

func confirm() bool {
	fmt.Print("\nType yes to continue: ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')

	return err == nil && strings.TrimSpace(answer) == "yes"
}
