// Package api is a thin, typed HTTP client for the GophKeeper server.
//
// It performs no retries and no encryption; every method maps one-to-one
// to a server endpoint and returns a Go error on any non-2xx response.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"gophkeeper/internal/protocol"
)

// Client talks to a single GophKeeper server.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client for the given base URL, e.g. "http://localhost:8080".
// If hc is nil, a default client with a 30-second timeout is used.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: baseURL, http: hc}
}

// Register creates a new account and returns its user ID.
func (c *Client) Register(ctx context.Context, username, email, password string) (string, error) {
	var resp protocol.RegisterResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/auth/register", "",
		protocol.RegisterRequest{Username: username, Email: email, Password: password}, &resp)
	if err != nil {
		return "", err
	}
	return resp.UserID, nil
}

// Login exchanges credentials for a JWT.
func (c *Client) Login(ctx context.Context, username, password string) (token, userID string, err error) {
	var resp protocol.LoginResponse
	err = c.do(ctx, http.MethodPost, "/api/v1/auth/login", "",
		protocol.LoginRequest{Username: username, Password: password}, &resp)
	if err != nil {
		return "", "", err
	}
	return resp.Token, resp.UserID, nil
}

// Push sends the given items and returns the server's canonical versions.
func (c *Client) Push(ctx context.Context, token string, items []protocol.ItemDTO) ([]protocol.ItemDTO, error) {
	var resp protocol.PushResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/sync/push", token,
		protocol.PushRequest{Items: items}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// Pull returns items changed since the given time, plus the server's
// current timestamp.
func (c *Client) Pull(ctx context.Context, token string, since time.Time) ([]protocol.ItemDTO, time.Time, error) {
	var resp protocol.PullResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/sync/pull", token,
		protocol.PullRequest{Since: since}, &resp)
	if err != nil {
		return nil, time.Time{}, err
	}
	return resp.Items, resp.Now, nil
}

// List returns every live item owned by the authenticated user.
func (c *Client) List(ctx context.Context, token string) ([]protocol.ItemDTO, error) {
	var resp protocol.PushResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/items", token, nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// Get returns a single item by ID.
func (c *Client) Get(ctx context.Context, token, id string) (*protocol.ItemDTO, error) {
	var resp protocol.ItemDTO
	err := c.do(ctx, http.MethodGet, "/api/v1/items/"+id, token, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// Delete soft-deletes an item on the server.
func (c *Client) Delete(ctx context.Context, token, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/items/"+id, token, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("api: marshal: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("api: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var er protocol.ErrorResponse
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if err := json.Unmarshal(raw, &er); err == nil && er.Error != "" {
			return &HTTPError{Status: resp.StatusCode, Message: er.Error}
		}
		return &HTTPError{Status: resp.StatusCode, Message: string(raw)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("api: decode: %w", err)
	}
	return nil
}

// HTTPError is returned for any non-2xx response. It carries both the
// numeric status code and the server's human-readable message.
type HTTPError struct {
	Status  int
	Message string
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("api: HTTP %d: %s", e.Status, e.Message)
}

// IsStatus reports whether err is an *HTTPError with the given status.
func IsStatus(err error, status int) bool {
	he, ok := err.(*HTTPError)
	return ok && he.Status == status
}
