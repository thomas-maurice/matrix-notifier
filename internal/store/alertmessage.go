package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// AlertMessage maps an alert fingerprint to the Matrix message that
// announced it firing, so the resolved notification can edit that message
// in place instead of posting an unrelated new one.
type AlertMessage struct {
	ID          uint      `gorm:"primarykey"`
	CreatedAt   time.Time `gorm:"not null;index"`
	Fingerprint string    `gorm:"not null;index"`
	RoomID      string    `gorm:"not null"`
	EventID     string    `gorm:"not null"`
}

// RecordAlertMessages points each fingerprint at the message that just
// announced it, replacing any older mapping: a re-firing alert resolves
// against its newest announcement.
func (s *Store) RecordAlertMessages(ctx context.Context, roomID, eventID string, fingerprints []string) error {
	if len(fingerprints) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("room_id = ? AND fingerprint IN ?", roomID, fingerprints).Delete(&AlertMessage{}).Error; err != nil {
			return err
		}
		rows := make([]AlertMessage, 0, len(fingerprints))
		for _, fp := range fingerprints {
			rows = append(rows, AlertMessage{Fingerprint: fp, RoomID: roomID, EventID: eventID})
		}
		return tx.Create(&rows).Error
	})
}

// MapAlertMessages returns fingerprint → announcing event ID for the given
// fingerprints in a room. Mappings are deliberately NOT consumed:
// Alertmanager re-sends group notifications (group_interval, flapping),
// and each repeat must re-edit the same message idempotently instead of
// falling back to a duplicate standalone post. Mappings die by being
// re-pointed (alert fires again) or by retention pruning.
func (s *Store) MapAlertMessages(ctx context.Context, roomID string, fingerprints []string) (map[string]string, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}
	var rows []AlertMessage
	if err := s.db.WithContext(ctx).Where("room_id = ? AND fingerprint IN ?", roomID, fingerprints).Find(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Fingerprint] = r.EventID
	}
	return m, nil
}

// PruneAlertMessages drops mappings older than the cutoff: alerts that
// never resolved (or resolved while unmapped) must not accumulate forever.
func (s *Store) PruneAlertMessages(ctx context.Context, olderThan time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("created_at < ?", olderThan).Delete(&AlertMessage{})
	return res.RowsAffected, res.Error
}
