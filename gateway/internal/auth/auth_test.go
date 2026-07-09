package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMiddlewareRequiresValidBearerToken(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("first-token\nsecond-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	source, err := newFileTokenSource(tokenFile, 0)
	if err != nil {
		t.Fatalf("NewFileTokenSource() error = %v", err)
	}
	middleware, err := New(source)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var forwardedAuthorization string
	handler := middleware.Wrap(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forwardedAuthorization = request.Header.Get("Authorization")
		response.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic first-token", wantStatus: http.StatusUnauthorized},
		{name: "wrong token", header: "Bearer wrong", wantStatus: http.StatusUnauthorized},
		{name: "valid first token", header: "Bearer first-token", wantStatus: http.StatusNoContent},
		{name: "case insensitive scheme", header: "bearer second-token", wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/models/test/v1/models", nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusUnauthorized {
				assertAuthError(t, response, "invalid_api_key")
				if response.Header().Get("WWW-Authenticate") == "" {
					t.Error("WWW-Authenticate header is missing")
				}
			}
		})
	}
	if forwardedAuthorization != "" {
		t.Errorf("Authorization was forwarded to runtime: %q", forwardedAuthorization)
	}
}

func TestFileTokenSourceReloadsWithoutRestart(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("old-token"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	source, err := newFileTokenSource(tokenFile, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("NewFileTokenSource() error = %v", err)
	}
	middleware, err := New(source)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := middleware.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))

	assertTokenStatus(t, handler, "old-token", http.StatusNoContent)
	if err := os.WriteFile(tokenFile, []byte("new-token"), 0o600); err != nil {
		t.Fatalf("rotate token file: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	assertTokenStatus(t, handler, "old-token", http.StatusUnauthorized)
	assertTokenStatus(t, handler, "new-token", http.StatusNoContent)
}

func TestMiddlewareFailsClosedWhenTokenFileIsUnavailable(t *testing.T) {
	t.Parallel()
	source, err := newFileTokenSource(filepath.Join(t.TempDir(), "missing"), 0)
	if err != nil {
		t.Fatalf("NewFileTokenSource() error = %v", err)
	}
	middleware, err := New(source)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("request reached protected handler")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	assertAuthError(t, response, "auth_unavailable")
}

func TestFileTokenSourceCachesBetweenReloadIntervals(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("cached-token"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	source, err := newFileTokenSource(tokenFile, time.Hour)
	if err != nil {
		t.Fatalf("newFileTokenSource() error = %v", err)
	}
	tokens, err := source.Tokens()
	if err != nil {
		t.Fatalf("Tokens() error = %v", err)
	}
	if err := os.Remove(tokenFile); err != nil {
		t.Fatalf("remove token file: %v", err)
	}
	cached, err := source.Tokens()
	if err != nil {
		t.Fatalf("cached Tokens() error = %v", err)
	}
	if !reflect.DeepEqual(cached, tokens) {
		t.Errorf("cached tokens = %#v, want %#v", cached, tokens)
	}
}

func assertTokenStatus(t *testing.T, handler http.Handler, token string, want int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("token %q status = %d, want %d", token, response.Code, want)
	}
}

func assertAuthError(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if envelope.Error.Code != code {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, code)
	}
}
