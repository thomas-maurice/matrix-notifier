// Package outbox drains the durable notification queue: ingest handlers
// enqueue, the dispatcher delivers with exponential backoff so a Matrix
// outage or bot restart never loses a notification.
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/thomas-maurice/tocsin/internal/chart"
	"github.com/thomas-maurice/tocsin/internal/metrics"
	"github.com/thomas-maurice/tocsin/internal/notify"
	"github.com/thomas-maurice/tocsin/internal/store"
)

// Sender is what the dispatcher needs from the bot.
type Sender interface {
	notify.Sender
	SendWithImage(ctx context.Context, roomID string, n notify.Notification, filename string, png []byte) (eventID string, err error)
	SendEdit(ctx context.Context, roomID, targetEventID string, n notify.Notification) error
}

const (
	// pollInterval bounds how stale a due entry can go unnoticed when no
	// kick arrives (e.g. after an error path or across restarts).
	pollInterval = 15 * time.Second
	// backoffBase doubles per attempt up to backoffCap.
	backoffBase = 10 * time.Second
	backoffCap  = 10 * time.Minute
	// attemptTimeout bounds a single delivery attempt, chart render included.
	attemptTimeout = 60 * time.Second
	pruneInterval  = time.Hour
	batchSize      = 32
	// maxErrorLen keeps stored delivery errors readable, not unbounded.
	maxErrorLen = 500
)

// maxPendingAge is how long an entry may keep failing before it is marked
// failed for good (still retryable by hand from the UI). A var so tests can
// exercise the give-up path without a day-long clock.
var maxPendingAge = 24 * time.Hour

// Dispatcher owns the drain loop. Enqueue from any goroutine; Run once.
type Dispatcher struct {
	log       *slog.Logger
	st        *store.Store
	sender    Sender
	charts    *chart.Client // nil disables chart rendering
	retention time.Duration
	kick      chan struct{}
}

func New(log *slog.Logger, st *store.Store, sender Sender, charts *chart.Client, retention time.Duration) *Dispatcher {
	return &Dispatcher{
		log:       log,
		st:        st,
		sender:    sender,
		charts:    charts,
		retention: retention,
		kick:      make(chan struct{}, 1),
	}
}

// Enqueue persists the entry and wakes the drain loop.
func (d *Dispatcher) Enqueue(ctx context.Context, e *store.OutboxEntry) error {
	if err := d.st.EnqueueOutbox(ctx, e); err != nil {
		return err
	}
	d.Kick()
	return nil
}

// Kick wakes the drain loop without blocking; a pending kick is enough.
func (d *Dispatcher) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run drains until the context is cancelled. It processes the backlog from
// before the restart immediately.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	prune := time.NewTicker(pruneInterval)
	defer prune.Stop()

	d.prune(ctx)
	d.drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.kick:
		case <-ticker.C:
		case <-prune.C:
			d.prune(ctx)
			continue
		}
		d.drain(ctx)
	}
}

// drain delivers every due entry. Entries that fail are rescheduled into
// the future, so the loop terminates once the due set is exhausted.
func (d *Dispatcher) drain(ctx context.Context) {
	for {
		entries, err := d.st.DueOutbox(ctx, time.Now(), batchSize)
		if err != nil {
			d.log.Error("reading outbox", "error", err)
			break
		}
		if len(entries) == 0 {
			break
		}
		for i := range entries {
			if ctx.Err() != nil {
				return
			}
			d.attempt(ctx, &entries[i])
		}
	}
	if pending, err := d.st.CountPendingOutbox(ctx); err == nil {
		metrics.OutboxPending.Set(float64(pending))
	}
}

func (d *Dispatcher) attempt(ctx context.Context, e *store.OutboxEntry) {
	err := d.deliver(ctx, e)
	attempts := e.Attempts + 1
	if err == nil {
		if err := d.st.MarkOutboxDelivered(ctx, e.ID, attempts); err != nil {
			d.log.Error("marking delivery", "id", e.ID, "error", err)
		}
		metrics.NotificationsDelivered.WithLabelValues(e.Channel, e.Kind).Inc()
		d.log.Info("notification delivered", "channel", e.Channel, "kind", e.Kind, "attempts", attempts)
		return
	}
	if time.Since(e.CreatedAt) >= maxPendingAge {
		if err := d.st.MarkOutboxFailed(ctx, e.ID, attempts, truncateError(err)); err != nil {
			d.log.Error("marking failure", "id", e.ID, "error", err)
		}
		metrics.NotificationsFailed.WithLabelValues(e.Channel, e.Kind).Inc()
		d.log.Error("giving up on notification", "channel", e.Channel, "kind", e.Kind, "attempts", attempts, "error", err)
		return
	}
	delay := backoff(attempts)
	if err := d.st.RescheduleOutbox(ctx, e.ID, attempts, time.Now().Add(delay), truncateError(err)); err != nil {
		d.log.Error("rescheduling delivery", "id", e.ID, "error", err)
	}
	d.log.Warn("delivery failed, will retry", "channel", e.Channel, "kind", e.Kind, "attempt", attempts, "retry_in", delay, "error", err)
}

// deliver performs one attempt: update the group's message in place when
// the entry only advances known alerts, else render the chart when the
// entry asks for one (best effort — a chart failure degrades to text, it
// never fails the delivery), then send.
func (d *Dispatcher) deliver(ctx context.Context, e *store.OutboxEntry) error {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	n := notify.Notification{Title: e.Title, Body: e.Body, Priority: e.Priority}

	if target := d.editTarget(ctx, e); target != "" {
		if err := d.sender.SendEdit(ctx, e.RoomID, target, n); err != nil {
			d.log.Warn("editing alert message failed, posting a new message", "channel", e.Channel, "event_id", target, "error", err)
		} else {
			d.log.Info("updated alert message in place", "channel", e.Channel, "event_id", target)
			return nil
		}
	}

	if e.ChartGeneratorURL != "" && e.ChartStartsAt != nil && d.charts != nil {
		start := time.Now()
		png, expr, err := d.charts.ChartForAlert(ctx, e.ChartGeneratorURL, *e.ChartStartsAt)
		metrics.ChartDuration.Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.ChartRenders.WithLabelValues("failure").Inc()
			d.log.Warn("chart rendering failed, delivering text only", "channel", e.Channel, "error", err)
		} else {
			metrics.ChartRenders.WithLabelValues("success").Inc()
			name := e.ChartAlertName
			if name == "" {
				name = "alert"
			}
			d.log.Debug("chart rendered", "channel", e.Channel, "expr", expr)
			// Image messages are never recorded for resolve-by-edit:
			// replacing an m.image with text renders unreliably across
			// clients, so their resolved counterpart posts normally.
			_, err := d.sender.SendWithImage(ctx, e.RoomID, n, fmt.Sprintf("%s.png", name), png)
			return err
		}
	}

	eventID, err := d.sender.Send(ctx, e.RoomID, n)
	if err != nil {
		return err
	}
	if e.Fingerprints != "" && eventID != "" {
		// Best effort: the notification IS delivered; a failed mapping only
		// costs the future edit, and failing here would re-send.
		if err := d.st.RecordAlertMessages(ctx, e.RoomID, eventID, splitFingerprints(e.Fingerprints)); err != nil {
			d.log.Error("recording alert message mapping", "channel", e.Channel, "error", err)
		}
	}
	return nil
}

// editTarget decides whether this entry may update an existing message in
// place instead of posting a new one. Allowed only when the entry resolves
// at least one known alert, introduces NO unannounced firing alert (a new
// alert must post — an edit pings nobody), and every involved fingerprint
// points at the same single message. Grouped alerts thus keep ONE live
// message: partial resolutions flip individual lines, the final resolution
// flips the whole message, and repeats re-edit idempotently. Anything
// else — unknown fingerprints, mappings split across messages, image
// originals — returns "" and posts normally.
func (d *Dispatcher) editTarget(ctx context.Context, e *store.OutboxEntry) string {
	resolved := splitFingerprints(e.ResolveFingerprints)
	if len(resolved) == 0 {
		return ""
	}
	firing := splitFingerprints(e.Fingerprints)
	m, err := d.st.MapAlertMessages(ctx, e.RoomID, append(firing, resolved...))
	if err != nil {
		d.log.Error("looking up alert messages", "channel", e.Channel, "error", err)
		return ""
	}
	target := ""
	for _, fp := range firing {
		eventID, ok := m[fp]
		if !ok {
			return "" // unannounced firing alert: it must post, not edit
		}
		if target == "" {
			target = eventID
		} else if target != eventID {
			return ""
		}
	}
	resolvedKnown := false
	for _, fp := range resolved {
		eventID, ok := m[fp]
		if !ok {
			continue
		}
		resolvedKnown = true
		if target == "" {
			target = eventID
		} else if target != eventID {
			return ""
		}
	}
	if !resolvedKnown {
		return ""
	}
	return target
}

func splitFingerprints(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

func (d *Dispatcher) prune(ctx context.Context) {
	cutoff := time.Now().Add(-d.retention)
	n, err := d.st.PruneOutbox(ctx, cutoff)
	if err != nil {
		d.log.Error("pruning outbox", "error", err)
		return
	}
	if n > 0 {
		d.log.Info("pruned delivery history", "entries", n)
	}
	if n, err := d.st.PruneAlertMessages(ctx, cutoff); err != nil {
		d.log.Error("pruning alert message mappings", "error", err)
	} else if n > 0 {
		d.log.Info("pruned alert message mappings", "entries", n)
	}
}

func backoff(attempts int) time.Duration {
	delay := backoffBase
	for i := 1; i < attempts && delay < backoffCap; i++ {
		delay *= 2
	}
	return min(delay, backoffCap)
}

func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > maxErrorLen {
		return msg[:maxErrorLen] + "…"
	}
	return msg
}
