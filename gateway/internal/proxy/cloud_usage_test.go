package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brassinai/inferops/gateway/internal/routing"
	"github.com/brassinai/inferops/internal/cloudusage"
)

type usageRoundTrip func(*http.Request) (*http.Response, error)

func (f usageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type interruptedUsageBody struct{ *bytes.Reader }

func (b interruptedUsageBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
func (interruptedUsageBody) Close() error { return nil }

func correlatedRequest() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/models/server/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"private request text"}]}`))
	for key, value := range map[string]string{"X-Request-ID": "request", "X-InferCloud-Gateway-Instance-Id": "gateway", "X-InferCloud-Offering-Id": "offering", "X-InferCloud-Provider-Account-Id": "provider", "X-InferCloud-Provider-Cluster-Id": "cluster", "X-InferCloud-Operation": "chat_completions", "X-InferCloud-Model": "model"} {
		r.Header.Set(key, value)
	}
	return r
}

func TestCloudUsageObservesJSONAndBrokenStreams(t *testing.T) {
	for _, scenario := range []string{"json", "stream", "broken-stream", "missing-usage"} {
		t.Run(scenario, func(t *testing.T) {
			queue, err := cloudusage.OpenQueue(t.TempDir(), 10)
			if err != nil {
				t.Fatal(err)
			}
			registry := routing.NewMemoryRegistry()
			if err := registry.Replace([]routing.Backend{{Name: "server", ModelID: "model", Endpoint: parseURL(t, "http://runtime.test"), State: routing.StateReady}}); err != nil {
				t.Fatal(err)
			}
			p, err := NewWithMetricsAndOptions(registry, nil, Options{UsageQueue: queue})
			if err != nil {
				t.Fatal(err)
			}
			body := `{"choices":[{"message":{"content":"private response text"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`
			contentType := "application/json"
			if strings.Contains(scenario, "stream") {
				body, contentType = "data: {\"choices\":[{\"delta\":{\"content\":\"private response text\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\n", "text/event-stream"
			}
			if scenario == "missing-usage" {
				body = `{"choices":[]}`
			}
			p.transport = usageRoundTrip(func(r *http.Request) (*http.Response, error) {
				for key := range r.Header {
					if strings.HasPrefix(strings.ToLower(key), "x-infercloud-") {
						t.Errorf("cloud identity metadata leaked to runtime: %s", key)
					}
				}
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(body))
				if scenario == "broken-stream" {
					reader = interruptedUsageBody{bytes.NewReader([]byte(body))}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: reader}, nil
			})
			response := httptest.NewRecorder()
			p.ServeHTTP(response, correlatedRequest())
			if response.Body.String() != body {
				t.Fatal("usage observation changed consumer response bytes")
			}
			batch, err := queue.Batch(100)
			if err != nil || len(batch.Reports) != 1 || batch.Pending != 0 {
				t.Fatalf("terminal receipt not durable: %+v %v", batch, err)
			}
			r := batch.Reports[0].Report
			if scenario == "missing-usage" {
				if r.ReportPayload["usageSource"] != "unavailable" {
					t.Fatal("missing runtime counts were invented")
				}
			} else if r.InputTokens != 11 || r.OutputTokens != 7 || r.ReportPayload["usageSource"] != "runtime" {
				t.Fatalf("runtime usage was not observed: %+v", r)
			}
			if (scenario == "broken-stream" && r.Status != "StreamingFailed") || (scenario != "broken-stream" && r.Status != "Succeeded") {
				t.Fatalf("incorrect observed outcome: %s", r.Status)
			}
		})
	}
}

func TestFullUsageQueueRejectsBeforeRuntimeExecution(t *testing.T) {
	queue, err := cloudusage.OpenQueue(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	registry := routing.NewMemoryRegistry()
	if err := registry.Replace([]routing.Backend{{Name: "server", ModelID: "model", Endpoint: parseURL(t, "http://runtime.test"), State: routing.StateReady}}); err != nil {
		t.Fatal(err)
	}
	p, err := NewWithMetricsAndOptions(registry, nil, Options{UsageQueue: queue})
	if err != nil {
		t.Fatal(err)
	}
	report, _, _ := cloudusage.FromHeaders(correlatedRequest().Header)
	if _, err := queue.Begin(report); err != nil {
		t.Fatal(err)
	}
	p.transport = usageRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("runtime was called without a durable receipt reservation")
		return nil, io.ErrUnexpectedEOF
	})
	request := correlatedRequest()
	request.Header.Set("X-Request-ID", "another-request")
	response := httptest.NewRecorder()
	p.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("queue-full request was admitted: %d", response.Code)
	}
}

func TestCloudImageMessagesPassThroughWithRuntimeTokenAccounting(t *testing.T) {
	queue, err := cloudusage.OpenQueue(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	registry := routing.NewMemoryRegistry()
	if err := registry.Replace([]routing.Backend{{Name: "server", ModelID: "model", Endpoint: parseURL(t, "http://runtime.test"), State: routing.StateReady}}); err != nil {
		t.Fatal(err)
	}
	p, err := NewWithMetricsAndOptions(registry, nil, Options{UsageQueue: queue})
	if err != nil {
		t.Fatal(err)
	}
	const payload = `{"model":"model","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":[{"type":"text","text":"Describe it."},{"type":"image_url","image_url":{"url":"data:image/png;base64,c3ludGhldGljLWltYWdlLWJ5dGVz"}}]}]}`
	const runtimeResponse = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1536,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n"
	p.transport = usageRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload {
			t.Fatal("provider gateway changed multimodal request bytes", err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(runtimeResponse))}, nil
	})
	request := correlatedRequest()
	request.Body = io.NopCloser(strings.NewReader(payload))
	request.ContentLength = int64(len(payload))
	response := httptest.NewRecorder()
	p.ServeHTTP(response, request)
	batch, err := queue.Batch(10)
	if response.Code != 200 || response.Body.String() != runtimeResponse || err != nil || len(batch.Reports) != 1 {
		t.Fatal("vision request failed to retain its durable usage receipt", err)
	}
	report := batch.Reports[0].Report
	if report.InputTokens != 1536 || report.OutputTokens != 7 || report.ReportPayload["usageSource"] != "runtime" {
		t.Fatal("visual prompt usage was changed")
	}
	if strings.Contains(fmt.Sprint(batch.Reports[0]), "c3ludGhldGlj") {
		t.Fatal("image content leaked into usage receipt")
	}
}
