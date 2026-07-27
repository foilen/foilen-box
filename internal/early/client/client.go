// Package client is an HTTP client for https://developers.early.app/.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"foilen-box/internal/early/model"
)

const (
	baseURL = "https://api.early.app/api/v4"
	timeout = 30 * time.Second
)

// dateFormat matches Early's expected query-parameter timestamp layout.
const dateFormat = "2006-01-02T15:04:05.000"

// Client is a stateful, single-session Early API client: Connect stores a
// bearer token on the struct for subsequent calls, matching the original
// Java client's design (not safe for concurrent use by design).
type Client struct {
	httpClient *http.Client
	token      string
}

func New() *Client {
	return &Client{httpClient: &http.Client{Timeout: timeout}}
}

// https://developers.early.app/#72c12d32-0275-4491-859e-3be83bfaa3e9
func (c *Client) Connect(cfg model.ConfigEarly) error {
	reqBody, err := json.Marshal(model.SignInRequest{APIKey: cfg.APIKey, APISecret: cfg.APISecret})
	if err != nil {
		return fmt.Errorf("error during connection: %w", err)
	}

	body, err := c.post(baseURL+"/developer/sign-in", "", reqBody)
	if err != nil {
		return fmt.Errorf("error during connection: %w", err)
	}

	var signInResp model.SignInResponse
	if err := json.Unmarshal(body, &signInResp); err != nil {
		return fmt.Errorf("error during connection: %w", err)
	}
	if !signInResp.IsSuccess() {
		return fmt.Errorf("failed to sign in: %s", signInResp.Error.Message)
	}
	c.token = signInResp.Token
	return nil
}

// https://developers.early.app/#98b4f754-ebcd-4706-b9b0-93244c24e033
func (c *Client) TimeEntries(from, to time.Time) (*model.TimeEntriesResponse, error) {
	url := fmt.Sprintf("%s/time-entries/%s/%s", baseURL, from.Format(dateFormat), to.Format(dateFormat))
	body, err := c.get(url, c.token)
	if err != nil {
		return nil, fmt.Errorf("error during fetching time entries: %w", err)
	}
	var resp model.TimeEntriesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("error during fetching time entries: %w", err)
	}
	return &resp, nil
}

// https://developers.early.app/#ad0986b6-aae6-4b25-acc2-333b6822b6e6
func (c *Client) TimeEntryDelete(id string) (*model.Response, error) {
	url := fmt.Sprintf("%s/time-entries/%s", baseURL, id)
	body, err := c.delete(url, c.token)
	if err != nil {
		return nil, fmt.Errorf("error during deleting time entry: %w", err)
	}
	var resp model.Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("error during deleting time entry: %w", err)
	}
	return &resp, nil
}

// --- HTTP helpers ---
// Unlike Java's HttpURLConnection, Go's http.Client never errors on non-2xx
// responses, so the body is always read straight from resp.Body — no
// getErrorStream()-style fallback is needed.

func (c *Client) get(url, bearerToken string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setBearer(req, bearerToken)
	return c.do(req)
}

func (c *Client) post(url, bearerToken string, jsonBody []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setBearer(req, bearerToken)
	return c.do(req)
}

func (c *Client) delete(url, bearerToken string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	setBearer(req, bearerToken)
	return c.do(req)
}

func setBearer(req *http.Request, bearerToken string) {
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
