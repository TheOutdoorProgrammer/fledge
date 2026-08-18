package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdswhofish/fledge/internal/client"
	"github.com/nerdswhofish/fledge/internal/ipa"
	"github.com/nerdswhofish/fledge/internal/xcode"
)

func releaseCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("release", flag.ExitOnError)
	dir := flags.String("C", ".", "directory holding the workspace or project")
	scheme := flags.String("scheme", "", "scheme to build (default: the only one, or the one named after the project)")
	method := flags.String("method", xcode.MethodReleaseTesting, "export method: release-testing, debugging, enterprise")
	team := flags.String("team", "", "Apple team ID (default: Xcode's active team)")
	notes := flags.String("notes", "", "release note shown on the install page")
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	keep := flags.Bool("keep", false, "keep the archive and exported ipa instead of deleting them")
	quiet := flags.Bool("quiet", false, "hide xcodebuild output")
	if err := flags.Parse(args); err != nil {
		return err
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}

	project, err := xcode.Discover(ctx, root)
	if err != nil {
		if errors.Is(err, xcode.ErrNoProject) {
			return fmt.Errorf("%s holds no .xcworkspace or .xcodeproj", root)
		}
		return err
	}

	chosen, err := project.Scheme(*scheme)
	if err != nil {
		return err
	}

	teamID := *team
	if teamID == "" {
		// A missing team is not fatal: automatic signing resolves it from the
		// project. Guessing one from Keychain, however, silently builds against
		// the wrong team, so an unknown team stays unset.
		if resolved, err := xcode.ActiveTeam(ctx); err == nil {
			teamID = resolved
		}
	}

	log := io.Writer(os.Stderr)
	if *quiet {
		log = io.Discard
	}

	work, err := os.MkdirTemp("", "fledge-release-*")
	if err != nil {
		return err
	}
	if !*keep {
		defer func() { _ = os.RemoveAll(work) }()
	}

	archivePath := filepath.Join(work, project.Name()+".xcarchive")
	exportPath := filepath.Join(work, "export")

	fmt.Fprintf(os.Stderr, "==> archiving %s (scheme %s)\n", filepath.Base(project.Path), chosen)
	if err := xcode.Archive(ctx, xcode.ArchiveOptions{
		Project:     project,
		Scheme:      chosen,
		ArchivePath: archivePath,
	}, log); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "==> exporting (%s)\n", *method)
	packagePath, err := xcode.Export(ctx, xcode.ExportOptions{
		ArchivePath: archivePath,
		ExportPath:  exportPath,
		Method:      *method,
		TeamID:      teamID,
	}, log)
	if err != nil {
		return err
	}

	if err := warnAboutProfile(packagePath); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "==> publishing")
	build, err := api.Upload(ctx, packagePath, *notes)
	if err != nil {
		return err
	}

	if *keep {
		fmt.Fprintf(os.Stderr, "    build output kept in %s\n", work)
	}
	printRelease(build)

	return nil
}

func uploadCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("upload", flag.ExitOnError)
	notes := flags.String("notes", "", "release note shown on the install page")
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: fledge upload <file.ipa>")
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	if err := warnAboutProfile(flags.Arg(0)); err != nil {
		return err
	}

	build, err := api.Upload(ctx, flags.Arg(0), *notes)
	if err != nil {
		return err
	}
	printRelease(build)

	return nil
}

// warnAboutProfile says up front what would otherwise surface as an unexplained
// install failure on the device.
func warnAboutProfile(path string) error {
	app, err := ipa.Open(path)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}

	profile := app.Profile
	if profile == nil {
		fmt.Fprintln(os.Stderr, "    warning: no embedded provisioning profile")
		return nil
	}

	if !profile.Type.InstallsOverTheAir() {
		return errors.New("this archive is signed for the App Store and cannot be installed over the air")
	}
	if profile.Expired(time.Now()) {
		return fmt.Errorf("the embedded profile expired on %s", profile.Expires.Format("2006-01-02"))
	}
	if profile.Type == ipa.TypeDevelopment {
		fmt.Fprintln(os.Stderr, "    note: development signing, so devices need Developer Mode enabled")
		fmt.Fprintln(os.Stderr, "          export with -method release-testing to avoid that")
	}

	return nil
}

func printRelease(build *client.Build) {
	fmt.Printf("%s %s (%s)\n", build.Name, build.Version, build.Build)
	fmt.Printf("  bundle    %s\n", build.BundleID)
	fmt.Printf("  build     %s\n", build.BuildID)
	if build.Profile != "" {
		fmt.Printf("  profile   %s, expires %s, %d device(s)\n", build.Profile, build.Expires, build.Devices)
	}
	fmt.Printf("\nOpen this on the device, in Safari:\n  %s\n", build.PageURL)
}
