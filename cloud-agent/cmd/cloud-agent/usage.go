package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brassinai/inferops/internal/cloudusage"
	"github.com/prometheus/client_golang/prometheus"
)

type usageMetrics struct {
	delivered   prometheus.Counter
	failures    prometheus.Counter
	ready       prometheus.Gauge
	pending     prometheus.Gauge
	quarantined prometheus.Gauge
}

func newUsageMetrics(registry *prometheus.Registry) *usageMetrics {
	m := &usageMetrics{
		delivered:   prometheus.NewCounter(prometheus.CounterOpts{Name: "inferops_cloud_usage_reports_delivered_total", Help: "Provider receipts accepted and signature verified by InferCloud."}),
		failures:    prometheus.NewCounter(prometheus.CounterOpts{Name: "inferops_cloud_usage_report_failures_total", Help: "Provider receipt delivery or queue acknowledgement failures."}),
		ready:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "inferops_cloud_usage_queue_ready", Help: "Completed provider receipts waiting for cloud acknowledgement."}),
		pending:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "inferops_cloud_usage_queue_pending", Help: "Accepted provider requests without a durably recorded terminal observation."}),
		quarantined: prometheus.NewGauge(prometheus.GaugeOpts{Name: "inferops_cloud_usage_queue_quarantined", Help: "Permanently rejected provider receipts retained for operator review."}),
	}
	registry.MustRegister(m.delivered, m.failures, m.ready, m.pending, m.quarantined)
	return m
}

func (a *agent) usageLoop(ctx context.Context) {
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// Publish the current mesh key before using it to sign queued receipts.
		if !a.lastHeartbeatOK.Load() {
			delay = 5 * time.Second
			continue
		}
		batchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := a.deliverUsage(batchCtx)
		cancel()
		a.lastUsageOK.Store(err == nil)
		if err != nil {
			if a.usageMetrics != nil {
				a.usageMetrics.failures.Inc()
			}
			log.Printf("cloud usage report delivery failed: %v", err)
			if delay < 5*time.Second {
				delay = 5 * time.Second
			} else if delay < time.Minute {
				delay *= 2
				if delay > time.Minute {
					delay = time.Minute
				}
			}
			continue
		}
		delay = 5 * time.Second
	}
}

func (a *agent) deliverUsage(ctx context.Context) error {
	credential, err := readSecretFile(a.config.credentialFile)
	if err != nil {
		return err
	}
	key, err := readSecretFile(a.config.gatewayTokenFile)
	if err != nil {
		return err
	}
	var batch cloudusage.Batch
	if _, err := a.usageRequest(ctx, http.MethodGet, a.config.usageGatewayURL+"/internal/cloud/usage-reports", key, nil, http.StatusOK, &batch); err != nil {
		return fmt.Errorf("read gateway usage queue: %w", err)
	}
	if len(batch.Reports) > 100 {
		return errors.New("gateway usage batch exceeds the delivery limit")
	}
	if a.usageMetrics != nil {
		a.usageMetrics.ready.Set(float64(batch.Queued))
		a.usageMetrics.pending.Set(float64(batch.Pending))
		a.usageMetrics.quarantined.Set(float64(batch.Quarantined))
	}
	for _, receipt := range batch.Reports {
		report := receipt.Report
		if report.ProviderAccountID != a.config.providerAccountID || report.ProviderClusterID != a.config.providerClusterID {
			return errors.New("gateway receipt belongs to a different provider cluster; review gateway configuration")
		}
		digest, err := report.Digest()
		if err != nil || digest != receipt.Digest {
			return errors.New("gateway receipt digest is invalid; operator review required")
		}
		if err := report.Sign(key); err != nil {
			return err
		}
		// The path owns provider identity in the cloud API. The providerAccountId
		// body field is used only by the local queue and the signature contract.
		encoded, err := json.Marshal(report)
		if err != nil {
			return err
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &payload); err != nil {
			return err
		}
		delete(payload, "providerAccountId")
		var accepted struct {
			ProviderAccountID string `json:"providerAccountId"`
			RequestID         string `json:"requestId"`
			GatewayInstanceID string `json:"gatewayInstanceId"`
			SignatureVerified bool   `json:"signatureVerified"`
		}
		status, sendErr := a.usageRequest(ctx, http.MethodPost, a.config.cloudAPIURL+"/v1/provider-accounts/"+url.PathEscape(a.config.providerAccountID)+"/usage-reports", credential, payload, http.StatusAccepted, &accepted)
		errorCode := ""
		if sendErr != nil {
			if status != http.StatusBadRequest && status != http.StatusConflict && status != http.StatusUnprocessableEntity {
				return fmt.Errorf("submit provider receipt: %w", sendErr)
			}
			errorCode = fmt.Sprintf("cloud_http_%d", status)
			log.Printf("cloud usage receipt requires operator review: receipt_id=%s status=%d", receipt.ID, status)
		} else if !accepted.SignatureVerified || accepted.ProviderAccountID != report.ProviderAccountID || accepted.RequestID != report.RequestID || accepted.GatewayInstanceID != report.GatewayInstanceID {
			return errors.New("cloud did not confirm a verified receipt for this request; queue entry retained")
		}
		ack := map[string]string{"id": receipt.ID, "digest": receipt.Digest, "errorCode": errorCode}
		if _, err := a.usageRequest(ctx, http.MethodPost, a.config.usageGatewayURL+"/internal/cloud/usage-reports/ack", key, ack, http.StatusNoContent, nil); err != nil {
			return fmt.Errorf("acknowledge provider receipt: %w", err)
		}
		if sendErr == nil && a.usageMetrics != nil {
			a.usageMetrics.delivered.Inc()
		}
	}
	return nil
}

func (a *agent) usageRequest(ctx context.Context, method, target, token string, body any, want int, result any) (int, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return 0, errors.New("invalid usage delivery endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "inferops-cloud-agent")
	client := *a.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return 0, errors.New("usage delivery request failed; receipt retained for retry")
	}
	defer resp.Body.Close()
	// The API persists verified receipts with 202 before asynchronous ledger
	// reconciliation. Older published contracts specified 201; accept that too,
	// while still requiring the verified identity acknowledgement below.
	compatibleReceipt := want == http.StatusAccepted && resp.StatusCode == http.StatusCreated
	if resp.StatusCode != want && !compatibleReceipt {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("usage endpoint returned HTTP %d", resp.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(result); err != nil {
			return resp.StatusCode, errors.New("invalid usage delivery response; receipt retained")
		}
	}
	return resp.StatusCode, nil
}

func usageGatewayURLFromEnvironment(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", errors.New("INFEROPS_CLOUD_USAGE_GATEWAY_URL must be a gateway origin without credentials, path or query")
	}
	return value, nil
}
