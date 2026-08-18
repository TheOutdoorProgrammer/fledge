// Package store keeps uploaded builds on a filesystem, with one JSON sidecar
// per build so the directory stays readable without Fledge running.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/ipa"
)

// ErrNotFound is returned for any app, build or device that is not present.
var ErrNotFound = errors.New("not found")

// safeBundleID guards the one untrusted value that becomes a path component.
// A bundle identifier is read out of an uploaded archive, so it is attacker
// controlled even when the uploader is not.
var safeBundleID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// buildIDLength is how much of the package digest names a build. Twelve hex
// digits collide far beyond the number of builds a homelab will ever hold, and
// content addressing makes re-uploading the same archive idempotent.
const buildIDLength = 12

// Build is one uploaded package.
type Build struct {
	ID       string    `json:"id"`
	App      *ipa.App  `json:"app"`
	Uploaded time.Time `json:"uploaded"`
	Notes    string    `json:"notes,omitempty"`
	HasIcon  bool      `json:"has_icon"`
}

// Store is a filesystem-backed build repository.
type Store struct {
	root string
	mu   sync.RWMutex
}

// Open prepares a store rooted at dir, creating it when absent.
func Open(dir string) (*Store, error) {
	for _, sub := range []string{"apps", "devices"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			return nil, fmt.Errorf("store: prepare %s: %w", sub, err)
		}
	}
	return &Store{root: dir}, nil
}

// Put stores an uploaded package. The archive is staged to a temporary file
// first because the IPA cannot be inspected until it is seekable.
func (s *Store) Put(r io.Reader, notes string) (*Build, error) {
	staged, size, err := s.stage(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(staged) }()

	file, err := os.Open(staged)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	app, err := ipa.Read(file, size)
	if err != nil {
		return nil, err
	}
	if !safeBundleID.MatchString(app.BundleID) {
		return nil, fmt.Errorf("store: unusable bundle identifier %q", app.BundleID)
	}

	build := &Build{
		ID:       app.SHA256[:buildIDLength],
		App:      app,
		Uploaded: time.Now().UTC(),
		Notes:    notes,
		HasIcon:  len(app.Icon) > 0,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.buildDir(app.BundleID, build.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	if err := os.Rename(staged, filepath.Join(dir, "app.ipa")); err != nil {
		return nil, fmt.Errorf("store: place package: %w", err)
	}
	if len(app.Icon) > 0 {
		if err := writeAtomic(filepath.Join(dir, "icon.png"), app.Icon); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(filepath.Join(dir, "build.json"), build); err != nil {
		return nil, err
	}

	return build, nil
}

// stage copies an upload into the store's own directory so the later rename
// into place cannot cross a filesystem boundary.
func (s *Store) stage(r io.Reader) (string, int64, error) {
	temp, err := os.CreateTemp(s.root, ".upload-*")
	if err != nil {
		return "", 0, err
	}

	size, err := io.Copy(temp, r)
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return "", 0, err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return "", 0, err
	}

	return temp.Name(), size, nil
}

// Apps lists every bundle identifier holding at least one build.
func (s *Store) Apps() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(s.root, "apps"))
	if err != nil {
		return nil, err
	}

	apps := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			apps = append(apps, entry.Name())
		}
	}
	sort.Strings(apps)

	return apps, nil
}

// Builds lists an app's builds, newest first.
func (s *Store) Builds(bundleID string) ([]*Build, error) {
	if !safeBundleID.MatchString(bundleID) {
		return nil, ErrNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(s.root, "apps", bundleID, "builds"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	builds := make([]*Build, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		build, err := s.readBuild(bundleID, entry.Name())
		if err != nil {
			continue
		}
		builds = append(builds, build)
	}
	sort.Slice(builds, func(i, j int) bool {
		return builds[i].Uploaded.After(builds[j].Uploaded)
	})

	return builds, nil
}

// Build returns one build by its identifier.
func (s *Store) Build(bundleID, buildID string) (*Build, error) {
	if !safeBundleID.MatchString(bundleID) || !isHex(buildID) {
		return nil, ErrNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.readBuild(bundleID, buildID)
}

// Latest returns the most recently uploaded build for an app.
func (s *Store) Latest(bundleID string) (*Build, error) {
	builds, err := s.Builds(bundleID)
	if err != nil {
		return nil, err
	}
	if len(builds) == 0 {
		return nil, ErrNotFound
	}
	return builds[0], nil
}

// OpenPackage returns the stored archive together with its size, which the
// handler needs to serve a range-capable download.
func (s *Store) OpenPackage(bundleID, buildID string) (*os.File, int64, error) {
	if !safeBundleID.MatchString(bundleID) || !isHex(buildID) {
		return nil, 0, ErrNotFound
	}

	file, err := os.Open(filepath.Join(s.buildDir(bundleID, buildID), "app.ipa"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}

	return file, stat.Size(), nil
}

// Icon returns a build's app icon as a standard PNG.
func (s *Store) Icon(bundleID, buildID string) ([]byte, error) {
	if !safeBundleID.MatchString(bundleID) || !isHex(buildID) {
		return nil, ErrNotFound
	}

	icon, err := os.ReadFile(filepath.Join(s.buildDir(bundleID, buildID), "icon.png"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}

	return icon, err
}

// Delete removes one build and, when it was the last, the app directory too.
func (s *Store) Delete(bundleID, buildID string) error {
	if !safeBundleID.MatchString(bundleID) || !isHex(buildID) {
		return ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.RemoveAll(s.buildDir(bundleID, buildID)); err != nil {
		return err
	}

	buildsDir := filepath.Join(s.root, "apps", bundleID, "builds")
	if entries, err := os.ReadDir(buildsDir); err == nil && len(entries) == 0 {
		return os.RemoveAll(filepath.Join(s.root, "apps", bundleID))
	}

	return nil
}

// Prune deletes all but the newest keep builds of an app and reports how many
// it removed.
func (s *Store) Prune(bundleID string, keep int) (int, error) {
	if keep < 1 {
		return 0, errors.New("store: keep must be at least 1")
	}

	builds, err := s.Builds(bundleID)
	if err != nil {
		return 0, err
	}
	if len(builds) <= keep {
		return 0, nil
	}

	removed := 0
	for _, build := range builds[keep:] {
		if err := s.Delete(bundleID, build.ID); err != nil {
			return removed, err
		}
		removed++
	}

	return removed, nil
}

func (s *Store) buildDir(bundleID, buildID string) string {
	return filepath.Join(s.root, "apps", bundleID, "builds", buildID)
}

func (s *Store) readBuild(bundleID, buildID string) (*Build, error) {
	raw, err := os.ReadFile(filepath.Join(s.buildDir(bundleID, buildID), "build.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var build Build
	if err := json.Unmarshal(raw, &build); err != nil {
		return nil, fmt.Errorf("store: decode %s/%s: %w", bundleID, buildID, err)
	}

	return &build, nil
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'))
}

// writeAtomic replaces a file in one step so a reader never observes a partial
// write and a crash never leaves a truncated sidecar behind.
func writeAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return err
	}
	if err := os.Chmod(temp.Name(), 0o640); err != nil {
		_ = os.Remove(temp.Name())
		return err
	}

	return os.Rename(temp.Name(), path)
}

func isHex(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
