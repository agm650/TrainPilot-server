package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type Client struct {
	BaseURL      string
	HTTP         *http.Client
	AccessToken  string
	RefreshToken string
}

type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%s: %s", e.Status, e.Body) }

func New(base string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 10 * time.Second}}
}
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
func (c *Client) Refresh(ctx context.Context, refreshToken string) (service.TokenPair, error) {
	var pair service.TokenPair
	_, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/refresh", map[string]any{"refreshToken": refreshToken}, &pair)
	if err == nil {
		c.AccessToken = pair.AccessToken
		c.RefreshToken = pair.RefreshToken
	}
	return pair, err
}
func (c *Client) Login(ctx context.Context, username, password, clientID string) (service.TokenPair, error) {
	var pair service.TokenPair
	_, err := c.Do(ctx, "POST", "/api/v1/auth/login", map[string]any{"username": username, "password": password, "clientId": clientID, "clientName": "dccctl", "platform": "cli"}, &pair)
	if err == nil {
		c.AccessToken = pair.AccessToken
		c.RefreshToken = pair.RefreshToken
	}
	return pair, err
}
func (c *Client) Locomotives(ctx context.Context) ([]model.Locomotive, error) {
	var out struct {
		Items []model.Locomotive `json:"items"`
	}
	_, err := c.Do(ctx, "GET", "/api/v1/locomotives", nil, &out)
	return out.Items, err
}
func (c *Client) Locomotive(ctx context.Context, id string) (model.Locomotive, error) {
	var out model.Locomotive
	_, err := c.Do(ctx, http.MethodGet, "/api/v1/locomotives/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) CreateLocomotive(ctx context.Context, input model.LocomotiveInput) (model.Locomotive, error) {
	var out model.Locomotive
	_, err := c.Do(ctx, http.MethodPost, "/api/v1/locomotives", input, &out)
	return out, err
}
func (c *Client) UpdateLocomotive(ctx context.Context, id string, input model.LocomotiveInput) (model.Locomotive, error) {
	var out model.Locomotive
	_, err := c.Do(ctx, http.MethodPut, "/api/v1/locomotives/"+url.PathEscape(id), input, &out)
	return out, err
}
func (c *Client) DeleteLocomotive(ctx context.Context, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, "/api/v1/locomotives/"+url.PathEscape(id), nil, nil)
	return err
}
func (c *Client) Acquire(ctx context.Context, loco string) (model.ControlLease, error) {
	var out model.ControlLease
	_, err := c.Do(ctx, "POST", "/api/v1/locomotives/"+url.PathEscape(loco)+"/control-lease", nil, &out)
	return out, err
}
func (c *Client) Throttle(ctx context.Context, loco, lease string, speed int, direction station.Direction) error {
	_, err := c.Do(ctx, "PUT", "/api/v1/locomotives/"+url.PathEscape(loco)+"/throttle", map[string]any{"leaseId": lease, "speed": speed, "direction": direction}, nil)
	return err
}
func (c *Client) Release(ctx context.Context, lease string) error {
	_, err := c.Do(ctx, "DELETE", "/api/v1/control-leases/"+url.PathEscape(lease), nil, nil)
	return err
}

func (c *Client) ExportRollingStock(ctx context.Context) ([]byte, error) {
	return c.download(ctx, "/api/v1/exports/rolling-stock")
}

func (c *Client) ImportRollingStock(ctx context.Context, archive []byte, replace bool) error {
	return c.upload(ctx, "/api/v1/imports/rolling-stock?mode="+importMode(replace), archive)
}

func (c *Client) ExportLayout(ctx context.Context) ([]byte, error) {
	return c.download(ctx, "/api/v1/layout/export")
}

func (c *Client) ImportLayout(ctx context.Context, archive []byte, replace bool) error {
	return c.upload(ctx, "/api/v1/layout/import?mode="+importMode(replace), archive)
}

func (c *Client) download(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", resp.Status, string(data))
	}
	return data, nil
}

func (c *Client) upload(ctx context.Context, path string, archive []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(archive))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/vnd.dcc-control.package+zip")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, string(data))
	}
	return nil
}

func importMode(replace bool) string {
	if replace {
		return "replace"
	}
	return "merge"
}
