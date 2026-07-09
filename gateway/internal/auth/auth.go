// Package auth implements bearer-token authentication for the gateway.
package auth

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultReloadInterval = time.Second

// TokenSource returns the currently configured bearer tokens. Implementations
// may reload their backing store on every call.
type TokenSource interface {
	Tokens() ([]string, error)
}

// FileTokenSource reads newline-delimited bearer tokens from a file and caches
// them for a bounded interval. Projected Kubernetes Secret updates therefore
// take effect without restarting the gateway or adding filesystem I/O to every
// request.
type FileTokenSource struct {
	path           string
	reloadInterval time.Duration

	mu         sync.Mutex
	tokens     []string
	reloadErr  error
	nextReload time.Time
}

// NewFileTokenSource creates a token source backed by path.
func NewFileTokenSource(path string) (*FileTokenSource, error) {
	return newFileTokenSource(path, defaultReloadInterval)
}

func newFileTokenSource(path string, reloadInterval time.Duration) (*FileTokenSource, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("token file path is required")
	}
	if reloadInterval < 0 {
		return nil, errors.New("token reload interval must not be negative")
	}
	return &FileTokenSource{
		path:           path,
		reloadInterval: reloadInterval,
	}, nil
}

// Tokens returns the non-empty tokens currently stored in the configured file.
func (s *FileTokenSource) Tokens() ([]string, error) {
	if s == nil || s.path == "" {
		return nil, errors.New("token file source is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if !s.nextReload.IsZero() && now.Before(s.nextReload) {
		return append([]string(nil), s.tokens...), s.reloadErr
	}
	s.nextReload = now.Add(s.reloadInterval)
	content, err := os.ReadFile(s.path)
	if err != nil {
		s.tokens = nil
		s.reloadErr = err
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	tokens := make([]string, 0, len(lines))
	for _, line := range lines {
		token := strings.TrimSpace(line)
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		s.tokens = nil
		s.reloadErr = errors.New("token file contains no bearer tokens")
		return nil, s.reloadErr
	}
	s.tokens = tokens
	s.reloadErr = nil
	return append([]string(nil), tokens...), nil
}

// Middleware authenticates requests and removes gateway credentials before
// forwarding them to a runtime.
type Middleware struct {
	source TokenSource
}

// New creates bearer-token authentication middleware.
func New(source TokenSource) (*Middleware, error) {
	if source == nil {
		return nil, errors.New("token source is required")
	}
	return &Middleware{source: source}, nil
}

// Wrap protects next with bearer-token authentication.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if next == nil {
		panic("auth middleware requires a next handler")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		tokens, err := m.source.Tokens()
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "auth_unavailable", "gateway authentication is unavailable")
			return
		}
		presented, ok := bearerToken(request.Header.Values("Authorization"))
		if !ok || !matchesAny(presented, tokens) {
			response.Header().Set("WWW-Authenticate", `Bearer realm="inferops"`)
			writeError(response, http.StatusUnauthorized, "invalid_api_key", "missing or invalid bearer token")
			return
		}

		request = request.Clone(request.Context())
		request.Header.Del("Authorization")
		next.ServeHTTP(response, request)
	})
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func matchesAny(presented string, expected []string) bool {
	matched := 0
	for _, token := range expected {
		matched |= subtle.ConstantTimeCompare([]byte(presented), []byte(token))
	}
	return matched == 1
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    "authentication_error",
			"code":    code,
		},
	})
}
