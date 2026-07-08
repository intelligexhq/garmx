package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intelligexhq/garmx/internal/aggregator"
)

// Payload capture modes. They mirror the config values; the writer only needs
// to know how much of each transaction to persist.
const (
	payloadRequestResponse = "request-response"
	payloadRequest         = "request"
	payloadMetadata        = "metadata"
)

// Default writer tuning. The buffer is generous so a burst of calls is absorbed
// without dropping; batches keep transactions short under multi-writer WAL.
const (
	defaultBufferSize    = 1024
	defaultBatchSize     = 100
	defaultFlushInterval = time.Second
)

// Options configures a Writer. Zero values fall back to the defaults above,
// except Payload/MaxPayloadBytes which the caller resolves from config.
type Options struct {
	Payload         string
	MaxPayloadBytes int
	RedactKeys      []string
	BufferSize      int
	BatchSize       int
	FlushInterval   time.Duration
}

// Writer is an asynchronous, batched audit sink. Record hands an event to an
// internal buffer and returns immediately; a single background goroutine
// redacts, size-caps, batches, and inserts them. It satisfies
// aggregator.Sink.
//
// Durability is deliberately best-effort: if the buffer is full or the database
// is unavailable, events are dropped with a warning rather than blocking or
// crashing the gateway. The audit trail must never be able to take down the
// proxy it observes.
type Writer struct {
	store  *Store
	redact *Redactor
	logger *slog.Logger

	payload  string
	maxBytes int

	batchSize     int
	flushInterval time.Duration

	events chan aggregator.Event
	stop   chan struct{}
	wg     sync.WaitGroup

	stopped atomic.Bool
	dropped atomic.Int64
}

// NewWriter starts a Writer over store and begins its background loop. Close
// must be called to flush and release the store.
func NewWriter(store *Store, opts Options, logger *slog.Logger) *Writer {
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = defaultBufferSize
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	flush := opts.FlushInterval
	if flush <= 0 {
		flush = defaultFlushInterval
	}
	w := &Writer{
		store:         store,
		redact:        NewRedactor(opts.RedactKeys),
		logger:        logger.With("component", "audit"),
		payload:       opts.Payload,
		maxBytes:      opts.MaxPayloadBytes,
		batchSize:     batchSize,
		flushInterval: flush,
		events:        make(chan aggregator.Event, bufSize),
		stop:          make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

// Record enqueues an event without blocking. When the buffer is full (writer
// falling behind, or DB stalled) the event is dropped and counted; the count is
// logged periodically. This is the hot-path entry point, so it never blocks.
func (w *Writer) Record(e aggregator.Event) {
	if w.stopped.Load() {
		return
	}
	select {
	case w.events <- e:
	default:
		w.dropped.Add(1)
	}
}

// Close stops accepting events, flushes what is buffered (bounded by ctx), and
// closes the store. Safe to call once.
func (w *Writer) Close(ctx context.Context) error {
	if w.stopped.Swap(true) {
		return nil
	}
	close(w.stop)
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		w.logger.Warn("audit shutdown timed out before flush completed")
	}
	return w.store.Close()
}

// loop drains events into batches, flushing on the interval or when a batch
// fills. On stop it drains whatever remains and flushes once more. The events
// channel is never closed (Record could still race a late send), so shutdown is
// signalled via stop and a final non-blocking drain.
func (w *Writer) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()
	batch := make([]LogEntry, 0, w.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		w.flush(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-w.stop:
			w.drainInto(&batch)
			flush()
			return
		case e := <-w.events:
			batch = append(batch, w.toEntry(e))
			if len(batch) >= w.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
			if d := w.dropped.Swap(0); d > 0 {
				w.logger.Warn("dropped audit events (buffer full)", "count", d)
			}
		}
	}
}

// drainInto pulls any buffered events into batch without blocking.
func (w *Writer) drainInto(batch *[]LogEntry) {
	for {
		select {
		case e := <-w.events:
			*batch = append(*batch, w.toEntry(e))
		default:
			return
		}
	}
}

// flush inserts a batch, retrying once (busy_timeout already absorbs normal lock
// contention). Persistent failure drops the batch with a warning — never fatal.
func (w *Writer) flush(batch []LogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.store.InsertBatch(ctx, batch); err != nil {
		if err2 := w.store.InsertBatch(ctx, batch); err2 != nil {
			w.logger.Warn("dropping audit batch after insert failure", "rows", len(batch), "err", err2)
		}
	}
}

// toEntry converts an event to a storable row, applying the payload mode,
// redaction, and per-payload size cap. payload_bytes records the combined
// redacted size before any truncation, and truncated is set if either payload
// exceeded the cap.
func (w *Writer) toEntry(e aggregator.Event) LogEntry {
	entry := LogEntry{
		SessionID:     e.SessionID,
		ClientName:    e.ClientName,
		ClientVersion: e.ClientVersion,
		ServerName:    e.Server,
		Method:        e.Method,
		ToolExposed:   e.ToolExposed,
		ToolOriginal:  e.ToolOriginal,
		RPCID:         e.RPCID,
		LatencyMS:     e.LatencyMS,
		ErrorCode:     e.ErrorCode,
		ErrorMessage:  capString(e.ErrorMessage, w.maxBytes),
	}
	var origBytes int64
	truncated := false
	capField := func(raw json.RawMessage) string {
		red := w.redact.Redact(raw)
		origBytes += int64(len(red))
		if w.maxBytes > 0 && len(red) > w.maxBytes {
			truncated = true
			return fmt.Sprintf(`{"_truncated":true,"_origBytes":%d}`, len(red))
		}
		return string(red)
	}
	switch w.payload {
	case payloadRequestResponse:
		if len(e.RequestParams) > 0 {
			entry.RequestPayload = capField(e.RequestParams)
		}
		if len(e.ResponseResult) > 0 {
			entry.ResponsePayload = capField(e.ResponseResult)
		}
	case payloadRequest:
		if len(e.RequestParams) > 0 {
			entry.RequestPayload = capField(e.RequestParams)
		}
	case payloadMetadata:
		// No payload bodies stored.
	}
	entry.PayloadBytes = origBytes
	entry.Truncated = truncated
	return entry
}

// capString bounds a plain string (e.g. an error message) to max bytes so a
// pathologically long upstream message can't bloat a row. max <= 0 means no cap.
func capString(s string, max int) string {
	if max > 0 && len(s) > max {
		return s[:max]
	}
	return s
}
