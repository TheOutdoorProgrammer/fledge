// Package xcode drives xcodebuild for the release command.
package xcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Export methods Xcode 26 accepts. The pre-15.3 spellings (development, ad-hoc,
// app-store) still work as deprecated aliases, but only these are current.
const (
	MethodDebugging      = "debugging"
	MethodReleaseTesting = "release-testing"
	MethodEnterprise     = "enterprise"
	MethodAppStore       = "app-store-connect"
)

// ErrNoProject is returned when a directory holds neither a workspace nor a
// project.
var ErrNoProject = errors.New("no .xcworkspace or .xcodeproj here")

// Project is a discovered Xcode workspace or project.
type Project struct {
	Path        string
	IsWorkspace bool
	Schemes     []string
}

// Name is the project's base name without its extension.
func (p *Project) Name() string {
	return strings.TrimSuffix(filepath.Base(p.Path), filepath.Ext(p.Path))
}

// flag returns the xcodebuild argument pair that selects this project.
func (p *Project) flag() []string {
	if p.IsWorkspace {
		return []string{"-workspace", p.Path}
	}
	return []string{"-project", p.Path}
}

// Open uses a workspace or project named explicitly, rather than searching.
func Open(ctx context.Context, path string) (*Project, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}

	project := &Project{
		Path:        path,
		IsWorkspace: filepath.Ext(path) == ".xcworkspace",
	}

	schemes, err := listSchemes(ctx, project)
	if err != nil {
		return nil, err
	}
	project.Schemes = schemes

	return project, nil
}

// Discover finds the buildable in dir and lists its schemes. A workspace wins
// over a project, matching what Xcode itself opens.
func Discover(ctx context.Context, dir string) (*Project, error) {
	workspaces, err := filepath.Glob(filepath.Join(dir, "*.xcworkspace"))
	if err != nil {
		return nil, err
	}
	projects, err := filepath.Glob(filepath.Join(dir, "*.xcodeproj"))
	if err != nil {
		return nil, err
	}

	project := &Project{}
	switch {
	case len(workspaces) > 0:
		sort.Strings(workspaces)
		project.Path, project.IsWorkspace = workspaces[0], true
	case len(projects) > 0:
		sort.Strings(projects)
		project.Path = projects[0]
	default:
		return nil, ErrNoProject
	}

	project.Schemes, err = listSchemes(ctx, project)
	if err != nil {
		return nil, err
	}

	return project, nil
}

// Scheme picks the scheme to build. An explicit name is validated rather than
// trusted, because xcodebuild's error for an unknown scheme is buried in a wall
// of output.
func (p *Project) Scheme(requested string) (string, error) {
	if len(p.Schemes) == 0 {
		return "", fmt.Errorf("%s declares no shared schemes", filepath.Base(p.Path))
	}

	if requested != "" {
		for _, scheme := range p.Schemes {
			if scheme == requested {
				return scheme, nil
			}
		}
		return "", fmt.Errorf("no scheme %q in %s (have %s)",
			requested, filepath.Base(p.Path), strings.Join(p.Schemes, ", "))
	}

	for _, scheme := range p.Schemes {
		if scheme == p.Name() {
			return scheme, nil
		}
	}
	if len(p.Schemes) == 1 {
		return p.Schemes[0], nil
	}

	return "", fmt.Errorf("%s has several schemes (%s); choose one with -scheme",
		filepath.Base(p.Path), strings.Join(p.Schemes, ", "))
}

func listSchemes(ctx context.Context, project *Project) ([]string, error) {
	args := append([]string{"-list", "-json"}, project.flag()...)
	output, err := exec.CommandContext(ctx, "xcodebuild", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("xcodebuild -list: %w", err)
	}

	var listing struct {
		Project   struct{ Schemes []string } `json:"project"`
		Workspace struct{ Schemes []string } `json:"workspace"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		return nil, fmt.Errorf("decode xcodebuild -list: %w", err)
	}

	if project.IsWorkspace {
		return listing.Workspace.Schemes, nil
	}

	return listing.Project.Schemes, nil
}

// ArchiveOptions describes one archive invocation.
type ArchiveOptions struct {
	Project     *Project
	Scheme      string
	ArchivePath string
	Destination string
}

// Archive builds an .xcarchive.
func Archive(ctx context.Context, opts ArchiveOptions, log io.Writer) error {
	destination := opts.Destination
	if destination == "" {
		destination = "generic/platform=iOS"
	}

	args := append([]string{"archive"}, opts.Project.flag()...)
	args = append(args,
		"-scheme", opts.Scheme,
		"-destination", destination,
		"-archivePath", opts.ArchivePath,
		"-allowProvisioningUpdates",
	)

	return run(ctx, log, "xcodebuild", args...)
}

// ExportOptions describes one export invocation.
type ExportOptions struct {
	ArchivePath string
	ExportPath  string
	Method      string
	TeamID      string
}

// Export turns an archive into a signed .ipa and returns its path.
func Export(ctx context.Context, opts ExportOptions, log io.Writer) (string, error) {
	plist, err := writeExportOptions(opts)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(plist) }()

	err = run(ctx, log, "xcodebuild", "-exportArchive",
		"-archivePath", opts.ArchivePath,
		"-exportOptionsPlist", plist,
		"-exportPath", opts.ExportPath,
		"-allowProvisioningUpdates",
	)
	if err != nil {
		return "", err
	}

	packages, err := filepath.Glob(filepath.Join(opts.ExportPath, "*.ipa"))
	if err != nil {
		return "", err
	}
	if len(packages) == 0 {
		return "", fmt.Errorf("export produced no .ipa in %s", opts.ExportPath)
	}

	return packages[0], nil
}

// writeExportOptions emits the plist xcodebuild wants. It is generated rather
// than committed so the export method and team cannot drift from the flags.
func writeExportOptions(opts ExportOptions) (string, error) {
	method := opts.Method
	if method == "" {
		method = MethodReleaseTesting
	}

	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>method</key><string>` + method + `</string>
	<key>signingStyle</key><string>automatic</string>
	<key>stripSwiftSymbols</key><true/>
	<key>thinning</key><string>&lt;none&gt;</string>
`
	if opts.TeamID != "" {
		body += "\t<key>teamID</key><string>" + opts.TeamID + "</string>\n"
	}
	body += "</dict>\n</plist>\n"

	file, err := os.CreateTemp("", "fledge-export-*.plist")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		return "", err
	}

	return file.Name(), file.Close()
}

// ActiveTeam reads the team Xcode is currently signed in as. Picking the first
// signing identity out of Keychain instead is a known way to build against a
// stale team and get an opaque "No Account for Team" failure.
func ActiveTeam(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "defaults", "read",
		"com.apple.dt.Xcode", "IDEProvisioningTeamByIdentifier").Output()
	if err != nil {
		return "", fmt.Errorf("read Xcode's active team: %w", err)
	}

	for _, line := range strings.Split(string(output), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok && strings.Contains(key, "teamID") {
			return strings.Trim(strings.TrimSpace(value), `";`), nil
		}
	}

	return "", errors.New("xcode has no active provisioning team")
}

func run(ctx context.Context, log io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = log
	cmd.Stderr = log

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, args[0], err)
	}

	return nil
}
