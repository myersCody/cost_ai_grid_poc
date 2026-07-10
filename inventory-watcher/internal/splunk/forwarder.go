package splunk

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metrics"
)

type Forwarder struct {
	store       *inventory.Store
	client      *http.Client
	hecURL      string
	hecToken    string
	index       string
	interval    time.Duration
	batchSize   int
	logger      *slog.Logger
}

func New(store *inventory.Store, hecURL, hecToken, index string, interval time.Duration, tlsInsecure bool, logger *slog.Logger) *Forwarder {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsInsecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user-controlled dev/PoC flag
	}

	return &Forwarder{
		store:     store,
		client:    &http.Client{Transport: transport, Timeout: 10 * time.Second},
		hecURL:    hecURL,
		hecToken:  hecToken,
		index:     index,
		interval:  interval,
		batchSize: 100,
		logger:    logger,
	}
}

func (f *Forwarder) Run(ctx context.Context) error {
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			f.sweep(ctx)
		}
	}
}

func (f *Forwarder) sweep(ctx context.Context) {
	start := time.Now()

	cursor, err := f.store.SplunkCursor(ctx)
	if err != nil {
		f.logger.Error("failed to read splunk cursor", "error", err)
		metrics.SplunkForwardErrors.Inc()
		return
	}

	consecutiveErrors := 0
	totalSent := 0

	for {
		if ctx.Err() != nil {
			return
		}

		rows, err := f.store.RawEventsSince(ctx, cursor, f.batchSize)
		if err != nil {
			f.logger.Error("failed to query raw events for splunk", "error", err)
			metrics.SplunkForwardErrors.Inc()
			return
		}

		if len(rows) == 0 {
			break
		}

		payload := f.buildBatch(rows)
		if err := f.post(ctx, payload); err != nil {
			f.logger.Error("splunk HEC post failed", "error", err, "batch_size", len(rows))
			metrics.SplunkForwardErrors.Inc()
			consecutiveErrors++
			if consecutiveErrors >= 3 {
				f.logger.Warn("splunk: 3 consecutive errors, abandoning sweep")
				break
			}
			continue
		}

		consecutiveErrors = 0
		maxID := rows[len(rows)-1].ID
		if err := f.store.AdvanceSplunkCursor(ctx, maxID); err != nil {
			f.logger.Error("failed to advance splunk cursor", "error", err)
			metrics.SplunkForwardErrors.Inc()
			return
		}

		cursor = maxID
		totalSent += len(rows)
		metrics.SplunkForwardTotal.Add(float64(len(rows)))

		if len(rows) < f.batchSize {
			break
		}
	}

	if totalSent > 0 {
		f.logger.Info("splunk forward sweep", "sent", totalSent, "duration", time.Since(start))
	}
	metrics.SplunkForwardDuration.Observe(time.Since(start).Seconds())
}

type hecEvent struct {
	Time       float64     `json:"time"`
	Host       string      `json:"host"`
	Source     string      `json:"source"`
	SourceType string      `json:"sourcetype"`
	Index      string      `json:"index,omitempty"`
	Event      interface{} `json:"event"`
}

func (f *Forwarder) buildBatch(rows []inventory.RawEventRow) []byte {
	var buf bytes.Buffer
	for _, row := range rows {
		evt := hecEvent{
			Time:       float64(row.EventTime.Unix()) + float64(row.EventTime.Nanosecond())/1e9,
			Host:       "cost-event-consumer",
			Source:     "cost-event-consumer",
			SourceType: "_json",
			Index:      f.index,
			Event:      row,
		}
		line, _ := json.Marshal(evt)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func (f *Forwarder) post(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", f.hecURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating HEC request: %w", err)
	}
	req.Header.Set("Authorization", "Splunk "+f.hecToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("HEC request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("HEC returned status %d", resp.StatusCode)
	}
	return nil
}
