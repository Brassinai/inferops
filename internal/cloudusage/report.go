// Package cloudusage implements provider-observed receipts and their durable
// delivery contract with InferCloud. It never stores request or response text.
package cloudusage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Report contains independently observed inference usage, scoped to cloud IDs.
type Report struct {
	ProviderAccountID       string         `json:"providerAccountId"`
	ProviderClusterID       string         `json:"providerClusterId"`
	OfferingID              string         `json:"offeringId"`
	RequestID               string         `json:"requestId"`
	GatewayInstanceID       string         `json:"gatewayInstanceId"`
	Operation               string         `json:"operation"`
	Model                   string         `json:"model"`
	Status                  string         `json:"status"`
	InputTokens             int            `json:"inputTokens"`
	OutputTokens            int            `json:"outputTokens"`
	StreamingDurationMillis int64          `json:"streamingDurationMillis"`
	LatencyMillis           int64          `json:"latencyMillis"`
	FailureReason           string         `json:"failureReason"`
	ObservedStartedAt       time.Time      `json:"observedStartedAt"`
	ObservedFinishedAt      time.Time      `json:"observedFinishedAt"`
	SigningKeyID            string         `json:"signingKeyId"`
	Signature               string         `json:"signature"`
	ReportPayload           map[string]any `json:"reportPayload"`
}

// FromHeaders accepts only complete cloud correlation metadata. Local requests
// without cloud metadata do not produce cloud receipts.
func FromHeaders(h http.Header) (Report, bool, error) {
	r := Report{
		ProviderAccountID: h.Get("X-InferCloud-Provider-Account-Id"),
		ProviderClusterID: h.Get("X-InferCloud-Provider-Cluster-Id"),
		OfferingID:        h.Get("X-InferCloud-Offering-Id"), RequestID: h.Get("X-Request-ID"),
		GatewayInstanceID: h.Get("X-InferCloud-Gateway-Instance-Id"),
		Operation:         h.Get("X-InferCloud-Operation"), Model: h.Get("X-InferCloud-Model"),
	}
	if r.OfferingID == "" && r.GatewayInstanceID == "" {
		return Report{}, false, nil
	}
	for _, value := range []string{r.ProviderAccountID, r.ProviderClusterID, r.OfferingID, r.RequestID, r.GatewayInstanceID, r.Operation, r.Model} {
		if value == "" || len(value) > 1024 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\x00") {
			return Report{}, true, errors.New("complete cloud correlation headers are required")
		}
	}
	switch r.Operation {
	case "chat_completions", "completions", "embeddings":
	default:
		return Report{}, true, errors.New("unsupported cloud inference operation")
	}
	r.ObservedStartedAt = time.Now().UTC().Truncate(time.Microsecond)
	r.ReportPayload = map[string]any{"usageSource": "unavailable"}
	return r, true, nil
}

// SigningBytes implements infercloud-provider-usage-v1. Map keys are sorted by
// encoding/json. Signing credentials and signatures are deliberately excluded.
func (r Report) SigningBytes() ([]byte, error) {
	return json.Marshal([]any{
		"infercloud-provider-usage-v1", r.ProviderAccountID, r.ProviderClusterID,
		r.OfferingID, r.RequestID, r.GatewayInstanceID, r.Operation, r.Model, r.Status,
		r.InputTokens, r.OutputTokens, r.StreamingDurationMillis, r.LatencyMillis,
		r.FailureReason, r.ObservedStartedAt.UnixMicro(), r.ObservedFinishedAt.UnixMicro(), r.ReportPayload,
	})
}

// Digest identifies the immutable observed payload, independently of key rotation.
func (r Report) Digest() (string, error) {
	payload, err := r.SigningBytes()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// Sign authenticates the report using the cluster's current mesh gateway key.
// A queued observation can therefore be delivered after credential rotation.
func (r *Report) Sign(token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("usage report signing key is empty")
	}
	payload, err := r.SigningBytes()
	if err != nil {
		return err
	}
	keyHash := sha256.Sum256([]byte(token))
	r.SigningKeyID = "mesh-hmac-sha256-v1:" + hex.EncodeToString(keyHash[:])
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write(payload)
	r.Signature = hex.EncodeToString(mac.Sum(nil))
	return nil
}
