package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

type Client struct{ http *http.Client }

func NewClient(socket string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
}
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin API %s: %s", resp.Status, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
func (c *Client) CreateUser(ctx context.Context, username, display, password string, role model.Role, mustChange, bootstrap bool) (model.User, error) {
	var u model.User
	err := c.do(ctx, "POST", "/admin/v1/users", map[string]any{"username": username, "displayName": display, "password": password, "role": role, "mustChangePassword": mustChange, "bootstrap": bootstrap}, &u)
	return u, err
}
func (c *Client) ListUsers(ctx context.Context) ([]model.User, error) {
	var out struct {
		Items []model.User `json:"items"`
	}
	err := c.do(ctx, "GET", "/admin/v1/users", nil, &out)
	return out.Items, err
}
func (c *Client) SetEnabled(ctx context.Context, username string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	return c.do(ctx, "POST", "/admin/v1/users/"+url.PathEscape(username)+"/"+action, nil, nil)
}
func (c *Client) SetRole(ctx context.Context, username string, role model.Role) error {
	return c.do(ctx, "PUT", "/admin/v1/users/"+url.PathEscape(username)+"/role", map[string]any{"role": role}, nil)
}
func (c *Client) SetPassword(ctx context.Context, username, password string, mustChange bool) error {
	return c.do(ctx, "PUT", "/admin/v1/users/"+url.PathEscape(username)+"/password", map[string]any{"password": password, "mustChange": mustChange}, nil)
}
