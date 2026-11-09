package cloudusage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testReport() Report {
	started, _ := time.Parse(time.RFC3339Nano, "2026-01-01T00:00:00.123456Z")
	return Report{ProviderAccountID: "provider", ProviderClusterID: "cluster", OfferingID: "offering", RequestID: "request", GatewayInstanceID: "gateway", Operation: "chat_completions", Model: "org/model", Status: "Succeeded", InputTokens: 11, OutputTokens: 7, LatencyMillis: 1000, ObservedStartedAt: started, ObservedFinishedAt: started.Add(time.Second), ReportPayload: map[string]any{"usageSource": "runtime"}}
}

func TestSignatureWireContract(t *testing.T) {
	data, err := os.ReadFile("testdata/signature-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Key, KeyID, Canonical, Signature string }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	r := testReport()
	payload, err := r.SigningBytes()
	if err != nil || string(payload) != fixture.Canonical {
		t.Fatalf("wire contract changed: %s %v", payload, err)
	}
	if err := r.Sign(fixture.Key); err != nil || r.SigningKeyID != fixture.KeyID || r.Signature != fixture.Signature {
		t.Fatal("signature does not match the independent wire vector")
	}
	digest, _ := r.Digest()
	if err := r.Sign("rotated-test-key"); err != nil {
		t.Fatal(err)
	}
	rotated, _ := r.Digest()
	if digest != rotated {
		t.Fatal("key rotation changed immutable observation identity")
	}
}

func TestUsageObserverChunkBoundariesAndMissingUsage(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"private response text\"}}]}\r\n\r\ndata: {\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n"
	for width := 1; width <= len(stream); width++ {
		o := Observer{Streaming: true}
		for at := 0; at < len(stream); at += width {
			end := at + width
			if end > len(stream) {
				end = len(stream)
			}
			_, _ = o.Write([]byte(stream[at:end]))
		}
		o.Finish()
		if !o.Measured || o.Input != 11 || o.Output != 0 || o.pending != nil {
			t.Fatalf("chunk size %d lost runtime usage: %+v", width, o)
		}
	}
	for _, body := range []string{`{"choices":[]}`, `{"usage":{"prompt_tokens":-1,"completion_tokens":2}}`, `{"usage":{"prompt_tokens":1}}`} {
		o := Observer{}
		_, _ = o.Write([]byte(body))
		o.Finish()
		if o.Measured {
			t.Fatalf("missing or invalid usage became measured: %s", body)
		}
	}
	o := Observer{Embedding: true}
	_, _ = o.Write([]byte(`{"usage":{"prompt_tokens":12}}`))
	o.Finish()
	if !o.Measured || o.Input != 12 || o.Output != 0 {
		t.Fatal("embedding prompt-only usage not measured")
	}
}

func TestDurableQueueRecoveryAndAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	q, err := OpenQueue(directory, 10)
	if err != nil {
		t.Fatal(err)
	}
	r := testReport()
	id, err := q.Begin(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Begin(r); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate request not rejected before execution: %v", err)
	}
	restarted, err := OpenQueue(directory, 10)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := restarted.Batch(100)
	if err != nil || batch.Pending != 1 || len(batch.Reports) != 0 {
		t.Fatalf("crashed incomplete request fabricated a terminal receipt: %+v %v", batch, err)
	}
	if err := q.Complete(id, r); err != nil {
		t.Fatal(err)
	}
	batch, err = restarted.Batch(100)
	if err != nil || len(batch.Reports) != 1 || batch.Pending != 0 {
		t.Fatalf("persisted receipt missing after restart: %+v %v", batch, err)
	}
	receipt := batch.Reports[0]
	if err := restarted.Acknowledge(id, "0000000000000000000000000000000000000000000000000000000000000000", ""); err == nil {
		t.Fatal("wrong digest deleted a receipt")
	}
	if err := restarted.Acknowledge(id, receipt.Digest, ""); err != nil {
		t.Fatal(err)
	}
	if err := q.Acknowledge(id, receipt.Digest, ""); err != nil {
		t.Fatal("lost acknowledgement response was not retryable")
	}
	batch, err = q.Batch(100)
	if err != nil || batch.Queued != 0 || batch.Pending != 0 {
		t.Fatalf("acknowledged receipt remains: %+v %v", batch, err)
	}
}

func TestSharedQueueNeverDropsReceiptsAtCapacity(t *testing.T) {
	directory := t.TempDir()
	first, err := OpenQueue(directory, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenQueue(directory, 1)
	if err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := testReport()
			r.RequestID = fmt.Sprintf("request-%d", i)
			queue := first
			if i%2 == 0 {
				queue = second
			}
			if _, err := queue.Begin(r); err == nil {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	batch, err := first.Batch(100)
	if err != nil || admitted.Load() != 1 || batch.Pending != 1 {
		t.Fatalf("shared queue over-admitted or dropped receipts: %d %+v %v", admitted.Load(), batch, err)
	}
}
