// Package asc is a minimal App Store Connect API client covering device
// registration and provisioning profiles.
//
// It is hand-written because the only Go client for this API stopped being
// maintained in 2021 and targets a version of the spec Apple has moved a long
// way past.
package asc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const baseURL = "https://api.appstoreconnect.apple.com"

// tokenLifetime stays inside Apple's ceiling, which rejects any assertion
// claiming more than twenty minutes.
const tokenLifetime = 15 * time.Minute

// DeviceLimit is how many devices of each type a membership year allows.
// Apple does not expose it through the API, and removing a device does not
// return its slot, so the count only ever rises until the year rolls over.
const DeviceLimit = 100

// Client talks to App Store Connect. The API key must hold the Admin role: an
// Ad Hoc profile is a distribution profile, which Developer cannot create.
type Client struct {
	issuerID string
	keyID    string
	key      *ecdsa.PrivateKey
	http     *http.Client
}

// New builds a client from an App Store Connect API key. The private key is the
// .p8 Apple issues, PEM encoded.
func New(issuerID, keyID string, privateKey []byte) (*Client, error) {
	block, _ := pem.Decode(privateKey)
	if block == nil {
		return nil, errors.New("asc: private key is not PEM encoded")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("asc: parse private key: %w", err)
	}

	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("asc: expected an ECDSA key, got %T", parsed)
	}

	return &Client{
		issuerID: issuerID,
		keyID:    keyID,
		key:      key,
		http:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// token mints a fresh assertion per request rather than caching one, because a
// fifteen-minute credential is not worth the invalidation problem.
func (c *Client) token() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(tokenLifetime).Unix(),
		"aud": "appstoreconnect-v1",
	}

	// An individual key identifies itself with sub, a team key with iss.
	if c.issuerID != "" {
		claims["iss"] = c.issuerID
	} else {
		claims["sub"] = "user"
	}

	assertion := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	assertion.Header["kid"] = c.keyID

	return assertion.SignedString(c.key)
}

// Device is a registered device.
type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UDID       string `json:"udid"`
	Platform   string `json:"platform"`
	Status     string `json:"status"`
	DeviceType string `json:"deviceClass"`
}

// Enabled reports whether the device may appear in a new profile.
func (d Device) Enabled() bool {
	return d.Status == "ENABLED"
}

// deviceAttributes omits every empty field, because Apple rejects an update
// carrying attributes it considers create-only: sending udid or platform on a
// PATCH is a 409 even when the value is unchanged.
type deviceAttributes struct {
	Name       string `json:"name,omitempty"`
	UDID       string `json:"udid,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Status     string `json:"status,omitempty"`
	DeviceType string `json:"deviceClass,omitempty"`
}

type deviceResource struct {
	Type       string           `json:"type"`
	ID         string           `json:"id,omitempty"`
	Attributes deviceAttributes `json:"attributes"`
}

// Devices lists every device on the team, including disabled ones, because a
// disabled device still occupies one of the hundred slots.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var devices []Device
	next := "/v1/devices?limit=200"

	for next != "" {
		var page struct {
			Data  []deviceResource `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}

		for _, item := range page.Data {
			devices = append(devices, Device{
				ID:         item.ID,
				Name:       item.Attributes.Name,
				UDID:       item.Attributes.UDID,
				Platform:   item.Attributes.Platform,
				Status:     item.Attributes.Status,
				DeviceType: item.Attributes.DeviceType,
			})
		}

		next = trimBase(page.Links.Next)
	}

	return devices, nil
}

// FindDevice returns the device with this UDID, or nil.
func (c *Client) FindDevice(ctx context.Context, udid string) (*Device, error) {
	var page struct {
		Data []deviceResource `json:"data"`
	}
	path := "/v1/devices?filter[udid]=" + url.QueryEscape(udid)
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, nil
	}

	item := page.Data[0]

	return &Device{
		ID:       item.ID,
		Name:     item.Attributes.Name,
		UDID:     item.Attributes.UDID,
		Platform: item.Attributes.Platform,
		Status:   item.Attributes.Status,
	}, nil
}

// RegisterDevice makes a device usable in profiles and reports whether a slot
// was consumed. Re-registering a UDID is a 409 rather than a no-op, so this
// looks first and re-enables a disabled device instead of adding one.
func (c *Client) RegisterDevice(ctx context.Context, name, udid, platform string) (device *Device, consumedSlot bool, err error) {
	existing, err := c.FindDevice(ctx, udid)
	if err != nil {
		return nil, false, err
	}

	if existing != nil {
		if existing.Enabled() {
			return existing, false, nil
		}
		updated, err := c.setDeviceStatus(ctx, existing.ID, "ENABLED")
		return updated, false, err
	}

	body := map[string]any{"data": deviceResource{
		Type:       "devices",
		Attributes: deviceAttributes{Name: name, UDID: udid, Platform: platform},
	}}

	var response struct {
		Data deviceResource `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/devices", body, &response); err != nil {
		return nil, false, err
	}

	return &Device{
		ID:       response.Data.ID,
		Name:     response.Data.Attributes.Name,
		UDID:     response.Data.Attributes.UDID,
		Platform: response.Data.Attributes.Platform,
		Status:   response.Data.Attributes.Status,
	}, true, nil
}

// RenameDevice changes a device's name on the team. It is the one write Fledge
// never performs on its own, because a page visit should not edit a developer
// account without being asked.
func (c *Client) RenameDevice(ctx context.Context, id, name string) (*Device, error) {
	return c.patchDevice(ctx, id, deviceAttributes{Name: name})
}

func (c *Client) setDeviceStatus(ctx context.Context, id, status string) (*Device, error) {
	return c.patchDevice(ctx, id, deviceAttributes{Status: status})
}

func (c *Client) patchDevice(ctx context.Context, id string, attributes deviceAttributes) (*Device, error) {
	body := map[string]any{"data": deviceResource{
		Type:       "devices",
		ID:         id,
		Attributes: attributes,
	}}

	var response struct {
		Data deviceResource `json:"data"`
	}
	if err := c.do(ctx, http.MethodPatch, "/v1/devices/"+id, body, &response); err != nil {
		return nil, err
	}

	return &Device{
		ID:       response.Data.ID,
		Name:     response.Data.Attributes.Name,
		UDID:     response.Data.Attributes.UDID,
		Platform: response.Data.Attributes.Platform,
		Status:   response.Data.Attributes.Status,
	}, nil
}

// Capacity is how much of the annual device allowance is gone.
type Capacity struct {
	Platform string
	Used     int
	Limit    int
}

// Remaining is how many devices of this type can still be registered this year.
func (c Capacity) Remaining() int {
	if c.Used >= c.Limit {
		return 0
	}
	return c.Limit - c.Used
}

// Capacity counts registered devices by class. Every device counts, enabled or
// not, and the count only resets when the membership year rolls over.
func (c *Client) Capacity(ctx context.Context) (map[string]Capacity, error) {
	devices, err := c.Devices(ctx)
	if err != nil {
		return nil, err
	}

	counts := map[string]Capacity{}
	for _, device := range devices {
		class := device.DeviceType
		if class == "" {
			class = device.Platform
		}
		entry := counts[class]
		entry.Platform, entry.Limit = class, DeviceLimit
		entry.Used++
		counts[class] = entry
	}

	return counts, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, baseURL+path, payload)
	if err != nil {
		return err
	}

	assertion, err := c.token()
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+assertion)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}

	if response.StatusCode >= 400 {
		return fmt.Errorf("app store connect %s %s: %s: %s",
			method, path, response.Status, appleErrors(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}

	return json.Unmarshal(raw, out)
}

// appleErrors flattens Apple's error envelope, which is far more readable than
// the raw JSON when a registration is rejected.
func appleErrors(raw []byte) string {
	var envelope struct {
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Errors) == 0 {
		return string(raw)
	}

	parts := make([]string, 0, len(envelope.Errors))
	for _, item := range envelope.Errors {
		parts = append(parts, strings.TrimSpace(item.Title+": "+item.Detail))
	}

	return strings.Join(parts, "; ")
}

func trimBase(link string) string {
	return strings.TrimPrefix(link, baseURL)
}
