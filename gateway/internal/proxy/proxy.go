// Package proxy implements the OpenAI-compatible gateway reverse proxy.
package proxy

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/brassinai/inferops/gateway/internal/routing"
)

const retryAfterSeconds = 5

// Proxy routes requests using a model registry.
type Proxy struct {
	registry  routing.Registry
	transport http.RoundTripper
	errorLog  *log.Logger
}

// New creates a gateway proxy backed by registry.
func New(registry routing.Registry) (*Proxy, error) {
	if registry == nil {
		return nil, errors.New("model registry is required")
	}
	return &Proxy{
		registry:  registry,
		transport: http.DefaultTransport,
		errorLog:  log.New(os.Stderr, "gateway proxy: ", log.LstdFlags),
	}, nil
}

// ServeHTTP resolves a model route, enforces lifecycle state, and forwards only
// OpenAI-compatible /v1 paths to a ready backend.
func (p *Proxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !canonicalRequestPath(request.URL) {
		writeError(response, http.StatusBadRequest, "invalid_path", "invalid_request_error", "request path must not contain escapes, backslashes, or traversal segments")
		return
	}
	backend, upstreamPath, found := p.registry.Lookup(request.URL.Path)
	if !found {
		writeError(response, http.StatusNotFound, "model_not_found", "unknown_model", "model route was not found")
		return
	}

	switch backend.State {
	case routing.StateReady:
		if upstreamPath != "/v1" && !strings.HasPrefix(upstreamPath, "/v1/") {
			writeError(response, http.StatusNotFound, "invalid_path", "invalid_request_error", "model routes accept only /v1 endpoints")
			return
		}
		p.forward(response, request, backend, upstreamPath)
	case routing.StateInactive:
		writeError(response, http.StatusConflict, "model_inactive", "inactive_model", stateMessage(backend, "model is inactive"))
	case routing.StateActivating:
		response.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(response, http.StatusServiceUnavailable, "model_activating", "activating_model", stateMessage(backend, "model is activating"))
	case routing.StateDraining:
		response.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(response, http.StatusServiceUnavailable, "model_draining", "unavailable_model", stateMessage(backend, "model is draining"))
	default:
		response.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(response, http.StatusServiceUnavailable, "model_unavailable", "unavailable_model", stateMessage(backend, "model is unavailable"))
	}
}

func canonicalRequestPath(requestURL *url.URL) bool {
	if requestURL == nil ||
		requestURL.Path == "" ||
		requestURL.RawPath != "" ||
		strings.Contains(requestURL.Path, "\\") {
		return false
	}
	withoutTrailingSlash := strings.TrimSuffix(requestURL.Path, "/")
	if withoutTrailingSlash == "" {
		withoutTrailingSlash = "/"
	}
	return path.Clean(requestURL.Path) == withoutTrailingSlash
}

func (p *Proxy) forward(
	response http.ResponseWriter,
	request *http.Request,
	backend routing.Backend,
	upstreamPath string,
) {
	reverseProxy := &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(backend.Endpoint)
			proxyRequest.SetXForwarded()
			proxyRequest.Out.URL.Path = joinURLPath(backend.Endpoint, upstreamPath)
			proxyRequest.Out.URL.RawPath = ""
		},
		Transport:     p.transport,
		FlushInterval: -1,
		ErrorLog:      p.errorLog,
		ModifyResponse: func(upstreamResponse *http.Response) error {
			upstreamResponse.Header.Set("X-Accel-Buffering", "no")
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, upstreamRequest *http.Request, err error) {
			if upstreamRequest.Context().Err() != nil {
				return
			}
			p.errorLog.Printf("model=%q upstream request failed: %v", backend.Name, err)
			writeError(writer, http.StatusBadGateway, "upstream_error", "api_error", "model runtime request failed")
		},
	}
	reverseProxy.ServeHTTP(response, request)
}

func joinURLPath(endpoint *url.URL, requestPath string) string {
	base := strings.TrimSuffix(endpoint.Path, "/")
	if requestPath == "" || requestPath == "/" {
		if base == "" {
			return "/"
		}
		return base + "/"
	}
	return base + "/" + strings.TrimPrefix(requestPath, "/")
}

func stateMessage(backend routing.Backend, fallback string) string {
	if strings.TrimSpace(backend.Message) != "" {
		return backend.Message
	}
	return fallback
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeError(response http.ResponseWriter, status int, code, errorType, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(errorEnvelope{
		Error: errorBody{
			Message: message,
			Type:    errorType,
			Code:    code,
		},
	})
}
