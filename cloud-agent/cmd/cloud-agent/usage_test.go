package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brassinai/inferops/internal/cloudusage"
)

func queuedUsage(t *testing.T) (*cloudusage.Queue, cloudusage.Report) {
	t.Helper()
	q, err := cloudusage.OpenQueue(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	r := cloudusage.Report{ProviderAccountID: "provider", ProviderClusterID: "cluster", OfferingID: "offering", RequestID: "request", GatewayInstanceID: "gateway", Operation: "chat_completions", Model: "model", Status: "Succeeded", InputTokens: 11, OutputTokens: 7, ObservedStartedAt: time.Now().Add(-time.Second).UTC(), ObservedFinishedAt: time.Now().UTC(), ReportPayload: map[string]any{"usageSource": "runtime"}}
	id, err := q.Begin(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(id, r); err != nil {
		t.Fatal(err)
	}
	return q, r
}

func usageAgent(t *testing.T, gatewayURL, cloudURL string) *agent {
	t.Helper()
	credential, key := filepath.Join(t.TempDir(), "credential"), filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(credential, []byte("test-agent-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("test-mesh-key"), 0600); err != nil {
		t.Fatal(err)
	}
	return &agent{config: config{providerAccountID: "provider", providerClusterID: "cluster", usageGatewayURL: gatewayURL, cloudAPIURL: cloudURL, credentialFile: credential, gatewayTokenFile: key}, client: &http.Client{Timeout: time.Second}}
}

func TestUsageDeliveryRecoversLostCloudResponse(t *testing.T) {
	q, _ := queuedUsage(t)
	gateway := httptest.NewServer(q)
	defer gateway.Close()
	var calls atomic.Int32
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-agent-credential" {
			t.Error("report was not sent with agent credentials")
		}
		var report cloudusage.Report
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		if report.ProviderAccountID != "" || report.Signature == "" || report.SigningKeyID == "" {
			t.Error("invalid usage report wire payload")
		}
		if calls.Add(1) == 1 {
			// Simulate acceptance followed by a lost response. The immutable
			// receipt must remain queued and be sent again with the same IDs.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"providerAccountId": "provider", "requestId": report.RequestID, "gatewayInstanceId": report.GatewayInstanceID, "signatureVerified": true})
	}))
	defer cloud.Close()
	a := usageAgent(t, gateway.URL, cloud.URL)
	if err := a.deliverUsage(context.Background()); err == nil {
		t.Fatal("lost response acknowledged and discarded the receipt")
	}
	batch, _ := q.Batch(100)
	if batch.Queued != 1 {
		t.Fatal("receipt did not survive uncertain cloud acceptance")
	}
	if err := a.deliverUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	batch, _ = q.Batch(100)
	if batch.Queued != 0 || calls.Load() != 2 {
		t.Fatalf("retry/acknowledgement failed: %+v calls=%d", batch, calls.Load())
	}
}

func TestUsageDeliveryRetainsTransientErrorsAndQuarantinesConflicts(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 409, 400, 201, 202} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			q, _ := queuedUsage(t)
			gateway := httptest.NewServer(q)
			defer gateway.Close()
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{}`)) // A 201 without verified identity is insufficient.
			}))
			defer cloud.Close()
			a := usageAgent(t, gateway.URL, cloud.URL)
			err := a.deliverUsage(context.Background())
			batch, _ := q.Batch(100)
			if status == 409 || status == 400 {
				if err != nil || batch.Queued != 0 || batch.Quarantined != 1 {
					t.Fatalf("permanent rejection was lost or blocked retries: %+v %v", batch, err)
				}
			} else if err == nil || batch.Queued != 1 || batch.Quarantined != 0 {
				t.Fatalf("unconfirmed receipt was discarded: %+v %v", batch, err)
			}
		})
	}
}
