package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	token    tokenSource
}

// NewHTTPDrainChecker creates a checker for the gateway /drainz URL.
func NewHTTPDrainChecker(endpoint string) (*HTTPDrainChecker, error) {
	return NewHTTPDrainCheckerWithTokenFile(endpoint, "")
}

// NewHTTPDrainCheckerWithTokenFile creates a checker that sends a bearer token
// read from tokenFile when querying a protected gateway /drainz endpoint.
func NewHTTPDrainCheckerWithTokenFile(endpoint, tokenFile string) (*HTTPDrainChecker, error) {
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
		token: newFileTokenSource(tokenFile),
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
	complete, err := queryDrainEndpoint(ctx, c.client, endpoint, namespace, model, c.token)
	if err != nil {
		return false, fmt.Errorf("query drain status: %w", err)
	}
	return complete, nil
}

// EndpointSliceDrainChecker aggregates /drainz status from every ready gateway
// endpoint behind a Service. It avoids load-balanced false positives when
// gateway replicas keep process-local active request counts.
type EndpointSliceDrainChecker struct {
	client       client.Client
	namespace    string
	serviceName  string
	scheme       string
	port         int32
	httpClient   *http.Client
	token        tokenSource
	endpointPath string
}

// EndpointSliceDrainCheckerConfig describes the gateway Service whose pod
// endpoints should be queried for aggregate drain status.
type EndpointSliceDrainCheckerConfig struct {
	Namespace   string
	ServiceName string
	Scheme      string
	Port        int32
	TokenFile   string
}

// NewEndpointSliceDrainChecker creates an aggregate drain checker backed by
// Kubernetes EndpointSlices for the gateway Service.
func NewEndpointSliceDrainChecker(
	kubernetesClient client.Client,
	config EndpointSliceDrainCheckerConfig,
) (*EndpointSliceDrainChecker, error) {
	if kubernetesClient == nil {
		return nil, errors.New("kubernetes client is required")
	}
	namespace := strings.TrimSpace(config.Namespace)
	if namespace == "" {
		return nil, errors.New("gateway drain status namespace is required")
	}
	serviceName := strings.TrimSpace(config.ServiceName)
	if serviceName == "" {
		return nil, errors.New("gateway drain status Service name is required")
	}
	scheme := strings.TrimSpace(config.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("gateway drain status scheme must be http or https")
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, fmt.Errorf("gateway drain status port must be between 1 and 65535")
	}
	return &EndpointSliceDrainChecker{
		client:      kubernetesClient,
		namespace:   namespace,
		serviceName: serviceName,
		scheme:      scheme,
		port:        config.Port,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		token:        newFileTokenSource(config.TokenFile),
		endpointPath: "/drainz",
	}, nil
}

// DrainComplete returns true only when every ready gateway endpoint reports the
// selected backend as draining with no active requests.
func (c *EndpointSliceDrainChecker) DrainComplete(ctx context.Context, namespace, model string) (bool, error) {
	if c == nil {
		return false, nil
	}
	addresses, err := c.gatewayEndpointAddresses(ctx)
	if err != nil {
		return false, err
	}
	if len(addresses) == 0 {
		return false, fmt.Errorf("gateway Service %s/%s has no ready endpoints", c.namespace, c.serviceName)
	}
	for _, address := range addresses {
		endpoint := &url.URL{
			Scheme: c.scheme,
			Host:   net.JoinHostPort(address, strconv.Itoa(int(c.port))),
			Path:   c.endpointPath,
		}
		complete, err := queryDrainEndpoint(ctx, c.httpClient, endpoint, namespace, model, c.token)
		if err != nil {
			return false, fmt.Errorf("query gateway endpoint %s: %w", address, err)
		}
		if !complete {
			return false, nil
		}
	}
	return true, nil
}

func (c *EndpointSliceDrainChecker) gatewayEndpointAddresses(ctx context.Context) ([]string, error) {
	var slices discoveryv1.EndpointSliceList
	if err := c.client.List(ctx, &slices, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("list gateway EndpointSlices: %w", err)
	}
	addressSet := make(map[string]struct{})
	for index := range slices.Items {
		endpointSlice := &slices.Items[index]
		if endpointSlice.Labels[discoveryv1.LabelServiceName] != c.serviceName ||
			endpointSlice.DeletionTimestamp != nil ||
			!endpointSliceHasPort(endpointSlice, c.port) {
			continue
		}
		for endpointIndex := range endpointSlice.Endpoints {
			endpoint := &endpointSlice.Endpoints[endpointIndex]
			if !endpointReady(endpoint) {
				continue
			}
			for _, address := range endpoint.Addresses {
				address = strings.TrimSpace(address)
				if address != "" {
					addressSet[address] = struct{}{}
				}
			}
		}
	}
	addresses := make([]string, 0, len(addressSet))
	for address := range addressSet {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return addresses, nil
}

func endpointSliceHasPort(endpointSlice *discoveryv1.EndpointSlice, port int32) bool {
	for _, endpointPort := range endpointSlice.Ports {
		if endpointPort.Port != nil && *endpointPort.Port == port {
			return true
		}
	}
	return false
}

func endpointReady(endpoint *discoveryv1.Endpoint) bool {
	if endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating {
		return false
	}
	return endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready
}

func queryDrainEndpoint(
	ctx context.Context,
	httpClient *http.Client,
	endpoint *url.URL,
	namespace, model string,
	token tokenSource,
) (bool, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	query := endpoint.Query()
	query.Set("namespace", namespace)
	query.Set("model", model)
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, fmt.Errorf("build drain status request: %w", err)
	}
	if token != nil {
		value, err := token.Token()
		if err != nil {
			return false, err
		}
		request.Header.Set("Authorization", "Bearer "+value)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d", response.StatusCode)
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

type tokenSource interface {
	Token() (string, error)
}

type fileTokenSource string

func newFileTokenSource(path string) tokenSource {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return fileTokenSource(path)
}

func (s fileTokenSource) Token() (string, error) {
	path := string(s)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read gateway drain status token file: %w", err)
	}
	for _, token := range strings.FieldsFunc(string(data), func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		token = strings.TrimSpace(token)
		if token != "" {
			return token, nil
		}
	}
	return "", errors.New("gateway drain status token file contains no token")
}
