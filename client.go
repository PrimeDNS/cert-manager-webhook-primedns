package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultAPIURL = "https://api.primedns.net/v1"

// apiClient talks to the PrimeDNS endpoint /dns/acme-challenge. The API creates and removes the TXT
// record with exactly the name it is given, so the name cert-manager resolved is sent as it is.
type apiClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
	sleep   func(time.Duration)
}

func newAPIClient(baseURL, apiKey string) *apiClient {
	if baseURL == "" {
		baseURL = defaultAPIURL
	}
	return &apiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
		sleep:   time.Sleep,
	}
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("PrimeDNS API HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

// Present creates the TXT record (the API treats a repeat of the same name and value as success).
func (c *apiClient) Present(ctx context.Context, name, value string) error {
	return c.do(ctx, http.MethodPost, name, value)
}

// CleanUp removes only the TXT record with this name and value.
func (c *apiClient) CleanUp(ctx context.Context, name, value string) error {
	return c.do(ctx, http.MethodDelete, name, value)
}

func (c *apiClient) do(ctx context.Context, method, name, value string) error {
	body, err := json.Marshal(map[string]string{"name": name, "value": value})
	if err != nil {
		return err
	}
	// One retry on a rate limit or a temporary server error, as the other PrimeDNS clients do.
	for attempt := 0; ; attempt++ {
		status, retryAfter, respBody, err := c.send(ctx, method, body)
		if err != nil {
			return err
		}
		if status >= 200 && status < 300 {
			return nil
		}
		if attempt == 0 && (status == 429 || status == 502 || status == 503 || status == 504) {
			c.sleep(retryAfter)
			continue
		}
		return parseAPIError(status, respBody)
	}
}

func (c *apiClient) send(ctx context.Context, method string, body []byte) (int, time.Duration, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/dns/acme-challenge", bytes.NewReader(body))
	if err != nil {
		return 0, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("PrimeDNS API request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("reading the PrimeDNS API response: %w", err)
	}
	return resp.StatusCode, retryAfterSeconds(resp.Header.Get("Retry-After")), data, nil
}

// retryAfterSeconds returns the wait for a Retry-After header: 5 s when it is missing or not a number, at most 60 s.
func retryAfterSeconds(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || n < 0 {
		n = 5
	}
	if n > 60 {
		n = 60
	}
	return time.Duration(n) * time.Second
}

func parseAPIError(status int, body []byte) error {
	var r struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &r)
	e := &apiError{Status: status, Code: r.Error.Code, Message: r.Error.Message}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 200 {
			e.Message = e.Message[:200]
		}
	}
	switch {
	case status == 401:
		e.Message += " (the key is wrong, revoked or expired)"
	case e.Code == "NAME_NOT_ALLOWED" || e.Code == "INSUFFICIENT_SCOPE":
		e.Message += " (the key needs the scope acme:name:<record name> for this name, or dns:write)"
	}
	return e
}

// isScopeError reports whether the API refused the key for this name (403 NAME_NOT_ALLOWED / INSUFFICIENT_SCOPE).
func isScopeError(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == 403 && (e.Code == "NAME_NOT_ALLOWED" || e.Code == "INSUFFICIENT_SCOPE")
}
