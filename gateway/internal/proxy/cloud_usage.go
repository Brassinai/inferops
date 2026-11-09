package proxy

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/brassinai/inferops/gateway/internal/routing"
	"github.com/brassinai/inferops/internal/cloudusage"
)

func (p *Proxy) observeCloudUsage(w http.ResponseWriter, r *http.Request, backend routing.Backend, upstreamPath string) (http.ResponseWriter, func(), error) {
	report, cloud, err := cloudusage.FromHeaders(r.Header)
	if err != nil || !cloud {
		return w, func() {}, err
	}
	paths := map[string]string{"chat_completions": "/v1/chat/completions", "completions": "/v1/completions", "embeddings": "/v1/embeddings"}
	if backend.ModelID == "" || report.Model != backend.ModelID || paths[report.Operation] != upstreamPath {
		return w, nil, errors.New("cloud request model or operation does not match the discovered server")
	}
	id, err := p.options.UsageQueue.Begin(report)
	if err != nil {
		return w, nil, err
	}
	writer := &usageResponseWriter{ResponseWriter: w, observer: cloudusage.Observer{Embedding: report.Operation == "embeddings"}}
	finish := func() {
		aborted := recover()
		writer.observer.Finish()
		finished := time.Now().UTC().Truncate(time.Microsecond)
		report.ObservedFinishedAt = finished
		report.LatencyMillis = finished.Sub(report.ObservedStartedAt).Milliseconds()
		report.InputTokens, report.OutputTokens = writer.observer.Input, writer.observer.Output
		if writer.observer.Measured {
			report.ReportPayload["usageSource"] = "runtime"
		}
		report.Status = "Succeeded"
		switch {
		case writer.observer.Streaming && (aborted != nil || writer.writeFailed || writer.readFailed):
			report.Status, report.FailureReason = "StreamingFailed", "streaming_failed"
		case writer.status == 0 && r.Context().Err() != nil:
			report.Status, report.FailureReason = "Canceled", "client_canceled"
		case aborted != nil || writer.writeFailed || writer.readFailed:
			report.Status, report.FailureReason = "Failed", "provider_response_interrupted"
		case writer.status == http.StatusTooManyRequests:
			report.Status = "RateLimited"
		case writer.status >= 500:
			report.Status = "Unavailable"
		case writer.status >= 400:
			report.Status = "Failed"
		}
		if writer.observer.Streaming && !writer.started.IsZero() {
			report.StreamingDurationMillis = finished.Sub(writer.started).Milliseconds()
		}
		persistErr := p.options.UsageQueue.Complete(id, report)
		if persistErr != nil {
			p.errorLog.Printf("cloud usage completion not persisted: receipt_id=%s; operator review required", id)
		}
		if aborted != nil {
			panic(aborted)
		}
		if persistErr != nil {
			panic(http.ErrAbortHandler)
		}
	}
	return writer, finish, nil
}

type usageResponseWriter struct {
	http.ResponseWriter
	observer    cloudusage.Observer
	status      int
	started     time.Time
	writeFailed bool
	readFailed  bool
}

type observedResponseBody struct {
	io.ReadCloser
	writer *usageResponseWriter
}

func (b *observedResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.writer.readFailed = true
	}
	return n, err
}

func (w *usageResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *usageResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status, w.started = status, time.Now().UTC()
	w.observer.Streaming = strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream")
	w.ResponseWriter.WriteHeader(status)
}

func (w *usageResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	_, _ = w.observer.Write(body[:n])
	if err != nil {
		w.writeFailed = true
	}
	return n, err
}

func (w *usageResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.writeFailed = true
	}
}
