// Package config loads server settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Defaults chosen so that a container with only FLEDGE_PUBLIC_URL and
// FLEDGE_UPLOAD_TOKEN set is a working server.
const (
	DefaultAddr      = ":8080"
	DefaultDataDir   = "/var/lib/fledge"
	DefaultTitle     = "Fledge"
	DefaultMaxUpload = 1 << 30
)

// Config is the server's runtime configuration.
type Config struct {
	Addr        string
	DataDir     string
	PublicURL   string
	UploadToken string
	Title       string
	MaxUpload   int64
	Apple       Apple
	Enroll      Enroll
}

// Enroll configures device registration. It stays off until a token is set,
// because registering a device spends one of a hundred annual Apple slots that
// removing it does not give back.
type Enroll struct {
	Token       string
	SigningCert []byte
	SigningKey  []byte
}

// Enabled reports whether device registration is switched on at all.
func (e Enroll) Enabled() bool {
	return e.Token != ""
}

// CanSign reports whether the configuration profile will be signed. Without a
// certificate iOS installs it anyway but labels it as unverified.
func (e Enroll) CanSign() bool {
	return len(e.SigningCert) > 0 && len(e.SigningKey) > 0
}

// Apple holds App Store Connect API credentials. All three fields are required
// together; leaving them empty disables device registration.
type Apple struct {
	IssuerID   string
	KeyID      string
	PrivateKey []byte
	TeamID     string
}

// Enabled reports whether Fledge can talk to App Store Connect.
func (a Apple) Enabled() bool {
	return a.IssuerID != "" && a.KeyID != "" && len(a.PrivateKey) > 0
}

// Load reads configuration from the environment and rejects anything that would
// produce a server that looks healthy but cannot actually install an app.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:        envOr("FLEDGE_ADDR", DefaultAddr),
		DataDir:     envOr("FLEDGE_DATA_DIR", DefaultDataDir),
		PublicURL:   strings.TrimSuffix(os.Getenv("FLEDGE_PUBLIC_URL"), "/"),
		UploadToken: os.Getenv("FLEDGE_UPLOAD_TOKEN"),
		Title:       envOr("FLEDGE_TITLE", DefaultTitle),
		MaxUpload:   DefaultMaxUpload,
	}

	if raw := os.Getenv("FLEDGE_MAX_UPLOAD"); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || size <= 0 {
			return nil, fmt.Errorf("config: FLEDGE_MAX_UPLOAD must be a positive byte count, got %q", raw)
		}
		cfg.MaxUpload = size
	}

	if err := cfg.loadApple(); err != nil {
		return nil, err
	}
	if err := cfg.loadEnroll(); err != nil {
		return nil, err
	}

	return cfg, cfg.validate()
}

func (c *Config) loadEnroll() error {
	c.Enroll.Token = os.Getenv("FLEDGE_ENROLL_TOKEN")

	cert, err := readPEM("FLEDGE_ENROLL_SIGNING_CERT")
	if err != nil {
		return err
	}
	key, err := readPEM("FLEDGE_ENROLL_SIGNING_KEY")
	if err != nil {
		return err
	}

	if (cert == nil) != (key == nil) {
		return errors.New("config: the enrolment signing certificate and key must be set together")
	}
	c.Enroll.SigningCert, c.Enroll.SigningKey = cert, key

	return nil
}

// readPEM prefers the file named by KEY_FILE, falling back to KEY itself.
func readPEM(key string) ([]byte, error) {
	if path := os.Getenv(key + "_FILE"); path != "" {
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read %s_FILE: %w", key, err)
		}
		return value, nil
	}
	if value := os.Getenv(key); value != "" {
		return []byte(value), nil
	}

	return nil, nil
}

func (c *Config) loadApple() error {
	c.Apple = Apple{
		IssuerID: os.Getenv("FLEDGE_ASC_ISSUER_ID"),
		KeyID:    os.Getenv("FLEDGE_ASC_KEY_ID"),
		TeamID:   os.Getenv("FLEDGE_ASC_TEAM_ID"),
	}

	switch {
	case os.Getenv("FLEDGE_ASC_PRIVATE_KEY_FILE") != "":
		key, err := os.ReadFile(os.Getenv("FLEDGE_ASC_PRIVATE_KEY_FILE"))
		if err != nil {
			return fmt.Errorf("config: read FLEDGE_ASC_PRIVATE_KEY_FILE: %w", err)
		}
		c.Apple.PrivateKey = key
	case os.Getenv("FLEDGE_ASC_PRIVATE_KEY") != "":
		c.Apple.PrivateKey = []byte(os.Getenv("FLEDGE_ASC_PRIVATE_KEY"))
	}

	partial := c.Apple.IssuerID != "" || c.Apple.KeyID != "" || len(c.Apple.PrivateKey) > 0
	if partial && !c.Apple.Enabled() {
		return errors.New("config: FLEDGE_ASC_ISSUER_ID, FLEDGE_ASC_KEY_ID and a private key must all be set together")
	}

	return nil
}

// validate fails startup rather than at install time. A manifest served over
// plain HTTP is refused by iOS with no diagnostic at all, so a public URL that
// is not HTTPS produces a server that looks healthy and installs nothing.
func (c *Config) validate() error {
	if c.PublicURL == "" {
		return errors.New("config: FLEDGE_PUBLIC_URL is required")
	}

	parsed, err := url.Parse(c.PublicURL)
	if err != nil {
		return fmt.Errorf("config: FLEDGE_PUBLIC_URL is not a URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("config: FLEDGE_PUBLIC_URL must be https, got %q: iOS refuses to install over plain HTTP", c.PublicURL)
	}
	if parsed.Host == "" {
		return errors.New("config: FLEDGE_PUBLIC_URL has no host")
	}
	if c.UploadToken == "" {
		return errors.New("config: FLEDGE_UPLOAD_TOKEN is required")
	}

	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
