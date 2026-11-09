package cloudusage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

var ErrDuplicate = errors.New("cloud request already observed; duplicate execution refused")

// Receipt is an immutable terminal observation ready for agent delivery.
type Receipt struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	Report    Report `json:"report"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// Batch includes queue health so missing completions and rejected reports are
// observable independently of successful cloud heartbeats.
type Batch struct {
	Reports     []Receipt `json:"reports"`
	Pending     int       `json:"pending"`
	Queued      int       `json:"queued"`
	Quarantined int       `json:"quarantined"`
}

// Queue persists observations on a mounted filesystem. Its OS lock coordinates
// replicas sharing a PVC; atomic renames and fsync protect process restarts.
type Queue struct {
	directory string
	limit     int
}

func OpenQueue(directory string, limit int) (*Queue, error) {
	if !filepath.IsAbs(directory) || limit < 1 {
		return nil, errors.New("absolute usage queue directory and positive limit are required")
	}
	for _, name := range []string{"accepted", "ready", "rejected"} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0700); err != nil {
			return nil, fmt.Errorf("create usage queue: %w", err)
		}
	}
	q := &Queue{directory: directory, limit: limit}
	if err := q.locked(func() error { return nil }); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) locked(fn func() error) error {
	f, err := os.OpenFile(filepath.Join(q.directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func receiptID(r Report) string {
	sum := sha256.Sum256([]byte(r.ProviderAccountID + "\x00" + r.RequestID + "\x00" + r.GatewayInstanceID))
	return hex.EncodeToString(sum[:])
}

// Begin durably reserves an observation before forwarding any billable request.
func (q *Queue) Begin(r Report) (string, error) {
	id := receiptID(r)
	err := q.locked(func() error {
		count := 0
		for _, state := range []string{"accepted", "ready", "rejected"} {
			entries, err := os.ReadDir(filepath.Join(q.directory, state))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Name() == id+".json" {
					return ErrDuplicate
				}
				if strings.HasSuffix(entry.Name(), ".json") {
					count++
				}
			}
		}
		if count >= q.limit {
			return errors.New("usage queue is full; report delivery must recover before more cloud traffic")
		}
		return q.write("accepted", id, Receipt{ID: id, Report: r})
	})
	return id, err
}

// Complete stores the terminal receipt before allowing the proxy to finish.
func (q *Queue) Complete(id string, r Report) error {
	if id != receiptID(r) {
		return errors.New("usage receipt identity changed")
	}
	digest, err := r.Digest()
	if err != nil {
		return err
	}
	return q.locked(func() error {
		data, err := os.ReadFile(filepath.Join(q.directory, "accepted", id+".json"))
		if err != nil {
			return err
		}
		var accepted Receipt
		if err := json.Unmarshal(data, &accepted); err != nil {
			return err
		}
		before := accepted.Report
		if before.ProviderClusterID != r.ProviderClusterID || before.OfferingID != r.OfferingID || before.Operation != r.Operation || before.Model != r.Model || !before.ObservedStartedAt.Equal(r.ObservedStartedAt) {
			return errors.New("accepted usage observation identity changed")
		}
		if err := q.write("ready", id, Receipt{ID: id, Digest: digest, Report: r}); err != nil {
			return err
		}
		return q.remove("accepted", id)
	})
}

func (q *Queue) write(state, id string, value Receipt) error {
	directory := filepath.Join(q.directory, state)
	f, err := os.CreateTemp(directory, ".receipt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(value); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(directory, id+".json")); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func syncDirectory(directory string) error {
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (q *Queue) remove(state, id string) error {
	if err := os.Remove(filepath.Join(q.directory, state, id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(filepath.Join(q.directory, state))
}

func (q *Queue) Batch(limit int) (Batch, error) {
	result := Batch{Reports: []Receipt{}}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	err := q.locked(func() error {
		for _, state := range []string{"ready", "accepted", "rejected"} {
			entries, err := os.ReadDir(filepath.Join(q.directory, state))
			if err != nil {
				return err
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				switch state {
				case "accepted":
					result.Pending++
				case "rejected":
					result.Quarantined++
				case "ready":
					result.Queued++
					if len(result.Reports) < limit {
						data, err := os.ReadFile(filepath.Join(q.directory, state, entry.Name()))
						if err != nil {
							return err
						}
						var receipt Receipt
						if err := json.Unmarshal(data, &receipt); err != nil {
							return errors.New("usage queue contains an unreadable receipt; operator review required")
						}
						result.Reports = append(result.Reports, receipt)
					}
				}
			}
		}
		return nil
	})
	return result, err
}

// Acknowledge removes only the exact payload accepted by InferCloud. Permanent
// rejections remain on disk for review and do not block unrelated receipts.
func (q *Queue) Acknowledge(id, digest, errorCode string) error {
	if len(id) != 64 || len(digest) != 64 {
		return errors.New("invalid receipt identity")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return errors.New("invalid receipt identity")
	}
	return q.locked(func() error {
		data, err := os.ReadFile(filepath.Join(q.directory, "ready", id+".json"))
		if os.IsNotExist(err) {
			return nil // Lost acknowledgement responses are safe to retry.
		}
		if err != nil {
			return err
		}
		var receipt Receipt
		if err := json.Unmarshal(data, &receipt); err != nil || receipt.Digest != digest {
			return errors.New("receipt acknowledgement does not match observed usage")
		}
		if errorCode != "" {
			receipt.ErrorCode = errorCode
			if err := q.write("rejected", id, receipt); err != nil {
				return err
			}
		}
		if err := q.remove("ready", id); err != nil {
			return err
		}
		return q.remove("accepted", id)
	})
}

// ServeHTTP must be wrapped in gateway authentication by its caller.
func (q *Queue) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/internal/cloud/usage-reports":
		batch, err := q.Batch(100)
		if err != nil {
			http.Error(w, "usage queue unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(batch)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/cloud/usage-reports/ack":
		var ack struct {
			ID        string `json:"id"`
			Digest    string `json:"digest"`
			ErrorCode string `json:"errorCode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&ack); err != nil || len(ack.ErrorCode) > 128 {
			http.Error(w, "invalid receipt acknowledgement", http.StatusBadRequest)
			return
		}
		if err := q.Acknowledge(ack.ID, ack.Digest, ack.ErrorCode); err != nil {
			http.Error(w, "receipt acknowledgement failed", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}
