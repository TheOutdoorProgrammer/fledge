package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/client"
	"github.com/theoutdoorprogrammer/fledge/internal/ipa"
	"github.com/theoutdoorprogrammer/fledge/internal/spec"
	"github.com/theoutdoorprogrammer/fledge/internal/xcode"
)

// openProject uses the project a spec names, or searches when it names none.
func openProject(ctx context.Context, root, named string) (*xcode.Project, error) {
	if named == "" {
		return xcode.Discover(ctx, root)
	}
	if !filepath.IsAbs(named) {
		named = filepath.Join(root, named)
	}

	return xcode.Open(ctx, named)
}

func releaseCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("release", flag.ExitOnError)
	dir := flags.String("C", ".", "directory holding the workspace or project")
	scheme := flags.String("scheme", "", "scheme to build (default: the only one, or the one named after the project)")
	// No flag default, so fledge.yaml can supply one; the fallback is applied
	// where the value is used.
	method := flags.String("method", "", "export method (default release-testing)")
	team := flags.String("team", "", "Apple team ID (default: Xcode's active team)")
	notes := flags.String("notes", "", "release note shown on the install page")
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	keep := flags.Bool("keep", false, "keep the archive and exported ipa instead of deleting them")
	quiet := flags.Bool("quiet", false, "hide xcodebuild output")
	if err := flags.Parse(args); err != nil {
		return err
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}

	config, err := spec.Find(root)
	if err != nil {
		return err
	}
	if config.Path != "" {
		fmt.Fprintf(os.Stderr, "==> using %s\n", config.Path)
	}

	api, err := newClient(spec.First(*server, os.Getenv("FLEDGE_URL"), config.Server), *token)
	if err != nil {
		return err
	}

	project, err := openProject(ctx, root, config.Build.Project)
	if err != nil {
		if errors.Is(err, xcode.ErrNoProject) {
			return fmt.Errorf("%s holds no .xcworkspace or .xcodeproj", root)
		}
		return err
	}

	chosen, err := project.Scheme(spec.First(*scheme, config.Build.Scheme))
	if err != nil {
		return err
	}

	teamID := spec.First(*team, config.Build.Team)
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

	exportMethod := spec.First(*method, config.Build.Method, xcode.MethodReleaseTesting)
	fmt.Fprintf(os.Stderr, "==> exporting (%s)\n", exportMethod)
	packagePath, err := xcode.Export(ctx, xcode.ExportOptions{
		ArchivePath: archivePath,
		ExportPath:  exportPath,
		Method:      exportMethod,
		TeamID:      teamID,
	}, log)
	if err != nil {
		return err
	}

	if err := warnAboutProfile(packagePath, config.FailOnDevelopmentSigning()); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "==> publishing")
	build, err := api.Upload(ctx, packagePath, spec.First(*notes, config.Notes))
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
	asJSON := flags.Bool("json", false, "print the published build as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	config, err := spec.Find(".")
	if err != nil {
		return err
	}

	// Both sources go through the same expansion. An argument used to be taken
	// literally, which made a caller passing a pattern, as the publish action
	// documents, fail with a confusing "no such file" naming the pattern.
	archive := flags.Arg(0)
	if archive == "" {
		archive = config.IPA
	}
	archive, err = soleMatch(archive)
	if err != nil {
		return err
	}

	api, err := newClient(spec.First(*server, os.Getenv("FLEDGE_URL"), config.Server), *token)
	if err != nil {
		return err
	}

	if err := warnAboutProfile(archive, config.FailOnDevelopmentSigning()); err != nil {
		return err
	}

	build, err := api.Upload(ctx, archive, spec.First(*notes, config.Notes))
	if err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(build)
	}
	printRelease(build)

	return nil
}

// soleMatch expands the spec's archive pattern, insisting on exactly one file
// so a stale export cannot be published in place of the intended build.
func soleMatch(pattern string) (string, error) {
	if pattern == "" {
		return "", errors.New("usage: fledge upload <file.ipa>, or set ipa: in fledge.yaml")
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no archive matched %s", pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%s matched %d files: %s", pattern, len(matches), strings.Join(matches, ", "))
	}
}

// warnAboutProfile says up front what would otherwise surface as an unexplained
// install failure on the device.
func warnAboutProfile(path string, strict bool) error {
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
		if strict {
			return errors.New("this archive is development signed, which forces every tester to enable Developer Mode; export with -method release-testing")
		}
		fmt.Fprintln(os.Stderr, "    note: development signing, so devices need Developer Mode enabled")
		fmt.Fprintln(os.Stderr, "          export with -method release-testing to avoid that")
	}

	return nil
}

// printRelease reports what was published. FLEDGE_SECURE withholds the URLs,
// because a public repository's build log should not disclose where a private
// server lives; the build is still identified, just not located.
func printRelease(build *client.Build) {
	fmt.Printf("%s %s (%s)\n", build.Name, build.Version, build.Build)
	fmt.Printf("  bundle    %s\n", build.BundleID)
	fmt.Printf("  build     %s\n", build.BuildID)
	if build.Profile != "" {
		fmt.Printf("  profile   %s, expires %s, %d device(s)\n", build.Profile, build.Expires, build.Devices)
	}

	if secure() {
		fmt.Println("\nPublished. The install URL is withheld because FLEDGE_SECURE is set.")
		return
	}

	fmt.Printf("\nOpen this on the device, in Safari:\n  %s\n", build.PageURL)
}

// secure reports whether URLs should be kept out of the output.
func secure() bool {
	switch os.Getenv("FLEDGE_SECURE") {
	case "", "0", "false":
		return false
	default:
		return true
	}
}
