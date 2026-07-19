package store

import (
	"context"
	"fmt"
	"time"
)

// DeliveryStatus is the lifecycle state of an outbox entry.
type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryFailed    DeliveryStatus = "failed"
)

// OutboxEntry is a queued (or historical) notification delivery. The table
// is both the durable send queue and the delivery history shown in the UI:
// pending rows are drained by the dispatcher, terminal rows are kept until
// pruned by retention.
type OutboxEntry struct {
	ID        uint      `gorm:"primarykey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
	// Channel and RoomID are denormalized so history survives channel
	// deletion or re-pointing.
	Channel  string `gorm:"not null;index"`
	RoomID   string `gorm:"not null"`
	Kind     string `gorm:"not null"`
	Title    string
	Body     string
	Priority int
	// Chart target (alertmanager notifications on chart-enabled channels).
	// The chart is rendered at send time so a delayed delivery charts data
	// around the alert, not a stale snapshot taken at ingest.
	ChartGeneratorURL string
	ChartStartsAt     *time.Time
	ChartAlertName    string
	// Fingerprints (comma-joined) of the firing alerts this entry
	// announces; recorded against the sent message so a later resolved
	// notification can edit it in place.
	Fingerprints string
	// ResolveFingerprints marks an all-resolved notification: the firing
	// messages mapped to these fingerprints are edited into the resolved
	// rendering instead of posting a new message.
	ResolveFingerprints string

	Status        DeliveryStatus `gorm:"not null;index:idx_outbox_due"`
	Attempts      int            `gorm:"not null;default:0"`
	NextAttemptAt time.Time      `gorm:"index:idx_outbox_due"`
	LastError     string
	DeliveredAt   *time.Time
}

// EnqueueOutbox persists a notification as pending, due immediately.
func (s *Store) EnqueueOutbox(ctx context.Context, e *OutboxEntry) error {
	e.Status = DeliveryPending
	e.NextAttemptAt = time.Now()
	return s.db.WithContext(ctx).Create(e).Error
}

// RecordDelivery inserts an entry that already reached a terminal state —
// synchronous sends (test notifications) record their outcome here so the
// history stays complete.
func (s *Store) RecordDelivery(ctx context.Context, e *OutboxEntry) error {
	if e.Status == DeliveryDelivered && e.DeliveredAt == nil {
		now := time.Now()
		e.DeliveredAt = &now
	}
	return s.db.WithContext(ctx).Create(e).Error
}

// DueOutbox returns pending entries whose next attempt is due, oldest first.
func (s *Store) DueOutbox(ctx context.Context, now time.Time, limit int) ([]OutboxEntry, error) {
	var entries []OutboxEntry
	err := s.db.WithContext(ctx).
		Where("status = ? AND next_attempt_at <= ?", DeliveryPending, now).
		Order("id").Limit(limit).Find(&entries).Error
	return entries, err
}

func (s *Store) MarkOutboxDelivered(ctx context.Context, id uint, attempts int) error {
	return s.db.WithContext(ctx).Model(&OutboxEntry{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":       DeliveryDelivered,
			"attempts":     attempts,
			"last_error":   "",
			"delivered_at": time.Now(),
		}).Error
}

// RescheduleOutbox records a failed attempt and when to try again.
func (s *Store) RescheduleOutbox(ctx context.Context, id uint, attempts int, nextAt time.Time, lastErr string) error {
	return s.db.WithContext(ctx).Model(&OutboxEntry{}).Where("id = ?", id).
		Updates(map[string]any{
			"attempts":        attempts,
			"next_attempt_at": nextAt,
			"last_error":      lastErr,
		}).Error
}

// MarkOutboxFailed gives up on an entry for good.
func (s *Store) MarkOutboxFailed(ctx context.Context, id uint, attempts int, lastErr string) error {
	return s.db.WithContext(ctx).Model(&OutboxEntry{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":     DeliveryFailed,
			"attempts":   attempts,
			"last_error": lastErr,
		}).Error
}

// ListOutbox returns the delivery history, newest first, optionally filtered
// by channel name.
func (s *Store) ListOutbox(ctx context.Context, channel string, limit int) ([]OutboxEntry, error) {
	q := s.db.WithContext(ctx).Order("id desc").Limit(limit)
	if channel != "" {
		q = q.Where("channel = ?", channel)
	}
	var entries []OutboxEntry
	return entries, q.Find(&entries).Error
}

// RequeueOutbox puts a failed entry back in the queue, due immediately.
// Only failed entries can be requeued: re-sending a delivered one would
// duplicate the notification.
func (s *Store) RequeueOutbox(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Model(&OutboxEntry{}).
		Where("id = ? AND status = ?", id, DeliveryFailed).
		Updates(map[string]any{
			"status":          DeliveryPending,
			"attempts":        0,
			"next_attempt_at": time.Now(),
			"last_error":      "",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("failed delivery %d: %w", id, ErrNotFound)
	}
	return nil
}

// PruneOutbox deletes terminal entries created before the cutoff. Pending
// entries are never pruned — the dispatcher fails them at max age first.
func (s *Store) PruneOutbox(ctx context.Context, olderThan time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("status IN ? AND created_at < ?", []DeliveryStatus{DeliveryDelivered, DeliveryFailed}, olderThan).
		Delete(&OutboxEntry{})
	return res.RowsAffected, res.Error
}

func (s *Store) CountPendingOutbox(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&OutboxEntry{}).
		Where("status = ?", DeliveryPending).Count(&n).Error
	return n, err
}
