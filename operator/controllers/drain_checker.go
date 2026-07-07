package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DrainChecker reports whether the gateway has finished forwarding requests
// already admitted for a backend. Returning false without an error means the
// drain is still in progress.
type DrainChecker interface {
	DrainComplete(ctx context.Context, namespace, model string) (bool, error)
}

// HTTPDrainChecker reads drain status from the InferOps gateway /drainz
// endpoint.
type HTTPDrainChecker struct {
	endpoint string
	client   *http.Client
}

// NewHTTPDrainChecker creates a checker for the gateway /drainz URL.
func NewHTTPDrainChecker(endpoint string) (*HTTPDrainChecker, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("drain status endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse drain status endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("drain status endpoint scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("drain status endpoint host is required")
	}
	return &HTTPDrainChecker{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
	}, nil
}

// DrainComplete reports true only when the gateway observes the backend as
// draining and has no active requests for it.
func (c *HTTPDrainChecker) DrainComplete(ctx context.Context, namespace, model string) (bool, error) {
	if c == nil {
		return false, nil
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return false, fmt.Errorf("parse drain status endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("namespace", namespace)
	query.Set("model", model)
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, fmt.Errorf("build drain status request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("query drain status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("query drain status: HTTP %d", response.StatusCode)
	}
	var payload struct {
		Backends []struct {
			DrainComplete bool `json:"drainComplete"`
		} `json:"backends"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return false, fmt.Errorf("decode drain status: %w", err)
	}
	if len(payload.Backends) != 1 {
		return false, fmt.Errorf("drain status returned %d backends for %s/%s", len(payload.Backends), namespace, model)
	}
	return payload.Backends[0].DrainComplete, nil
}
