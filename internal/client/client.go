// Package client talks to a Fledge server.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nerdswhofish/fledge/internal/store"
)

// Client is a Fledge API client.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New builds a client. Uploads are whole application archives, so the timeout
// has to be generous rather than the usual few seconds.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Minute},
	}
}

// Build is the server's account of a published build.
type Build struct {
	BundleID   string `json:"bundle_id"`
	BuildID    string `json:"build_id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Build      string `json:"build"`
	Profile    string `json:"profile"`
	Expires    string `json:"expires"`
	Devices    int    `json:"devices"`
	InstallURL string `json:"install_url"`
	PageURL    string `json:"page_url"`
}

// Upload streams an exported archive to the server.
func (c *Client) Upload(ctx context.Context, path, notes string) (*Build, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/builds", file)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.ContentLength = stat.Size()
	if notes != "" {
		request.Header.Set("X-Fledge-Notes", notes)
	}

	var build Build
	if err := c.do(request, &build); err != nil {
		return nil, err
	}

	return &build, nil
}

// App is one published application.
type App struct {
	BundleID string       `json:"bundle_id"`
	Builds   int          `json:"builds"`
	Latest   *store.Build `json:"latest"`
	PageURL  string       `json:"page_url"`
}

// Apps lists everything published.
func (c *Client) Apps(ctx context.Context) ([]App, error) {
	var response struct {
		Apps []App `json:"apps"`
	}
	if err := c.get(ctx, "/api/apps", &response); err != nil {
		return nil, err
	}

	return response.Apps, nil
}

// Builds lists one app's builds, newest first.
func (c *Client) Builds(ctx context.Context, bundleID string) ([]*store.Build, error) {
	var response struct {
		Builds []*store.Build `json:"builds"`
	}
	if err := c.get(ctx, "/api/apps/"+bundleID+"/builds", &response); err != nil {
		return nil, err
	}

	return response.Builds, nil
}

// Devices lists every enrolled device.
func (c *Client) Devices(ctx context.Context) ([]*store.Device, error) {
	var response struct {
		Devices []*store.Device `json:"devices"`
	}
	if err := c.get(ctx, "/api/devices", &response); err != nil {
		return nil, err
	}

	return response.Devices, nil
}

// Delete removes one published build.
func (c *Client) Delete(ctx context.Context, bundleID, buildID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.BaseURL+"/api/apps/"+bundleID+"/builds/"+buildID, nil)
	if err != nil {
		return err
	}

	return c.do(request, nil)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}

	return c.do(request, out)
}

func (c *Client) do(request *http.Request, out any) error {
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}

	if response.StatusCode >= 400 {
		return fmt.Errorf("%s: %s", response.Status, serverMessage(body))
	}
	if out == nil {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", request.URL.Path, err)
	}

	return nil
}

// serverMessage pulls the error detail out of a JSON body, falling back to the
// raw text when the server did not answer in JSON at all.
func serverMessage(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "no detail"
	}
	if len(trimmed) > 300 {
		trimmed = append(trimmed[:300], "..."...)
	}

	return string(trimmed)
}

// ErrNoServer is returned when the CLI has no server to talk to.
var ErrNoServer = errors.New("no server configured: set FLEDGE_URL or pass -server")
