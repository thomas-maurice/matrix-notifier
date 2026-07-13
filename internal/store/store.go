// Package store persists channels and ingest tokens with GORM, sharing the
// same database (SQLite or Postgres) as the E2EE crypto store.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
)

// TokenKind restricts which ingest endpoint a token may be used on.
type TokenKind string

const (
	KindAny          TokenKind = "any"
	KindGotify       TokenKind = "gotify"
	KindAlertmanager TokenKind = "alertmanager"
)

func ParseKind(s string) (TokenKind, error) {
	switch TokenKind(s) {
	case KindAny, KindGotify, KindAlertmanager:
		return TokenKind(s), nil
	case "":
		return KindAny, nil
	default:
		return "", fmt.Errorf("invalid token kind %q (want any, gotify or alertmanager)", s)
	}
}

// Channel maps a name to the Matrix room notifications are delivered to.
type Channel struct {
	ID        uint      `gorm:"primarykey"`
	CreatedAt time.Time `gorm:"not null"`
	Name      string    `gorm:"uniqueIndex;not null"`
	RoomID    string    `gorm:"not null"`
	// Chart attaches a rendered Prometheus chart to alertmanager
	// notifications on this channel (requires prometheus_url).
	Chart bool `gorm:"not null;default:false"`
}

// IngestToken authenticates notification producers and routes them to a
// channel. Only the SHA-256 of the token is stored.
type IngestToken struct {
	ID         uint      `gorm:"primarykey"`
	CreatedAt  time.Time `gorm:"not null"`
	Name       string    `gorm:"uniqueIndex;not null"`
	Kind       TokenKind `gorm:"not null;default:any"`
	TokenHash  string    `gorm:"uniqueIndex;not null"`
	ChannelID  uint      `gorm:"not null"`
	Channel    Channel   `gorm:"constraint:OnDelete:RESTRICT"`
	LastUsedAt *time.Time
}

var (
	ErrNotFound      = errors.New("not found")
	ErrChannelInUse  = errors.New("channel still has tokens")
	ErrAlreadyExists = errors.New("already exists")
)

type Store struct {
	db *gorm.DB
}

// Open connects to the configured database and migrates our tables. The
// crypto store manages its own tables in the same database independently.
func Open(cfg config.Database) (*Store, error) {
	var dialector gorm.Dialector
	switch cfg.Type {
	case "sqlite":
		// Same file as the crypto store: WAL + busy timeout so the two
		// connection pools coexist.
		dialector = sqlite.Open(fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", cfg.URI))
	case "postgres":
		dialector = postgres.Open(cfg.URI)
	default:
		return nil, fmt.Errorf("unsupported database type %q", cfg.Type)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("opening %s store: %w", cfg.Type, err)
	}
	if err := db.AutoMigrate(&Channel{}, &IngestToken{}); err != nil {
		return nil, fmt.Errorf("migrating schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) CreateChannel(ctx context.Context, name, roomID string, chart bool) (*Channel, error) {
	ch := Channel{Name: name, RoomID: roomID, Chart: chart}
	err := s.db.WithContext(ctx).Create(&ch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueViolation(err) {
			return nil, fmt.Errorf("channel %q: %w", name, ErrAlreadyExists)
		}
		return nil, err
	}
	return &ch, nil
}

func (s *Store) DeleteChannel(ctx context.Context, name string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ch Channel
		if err := tx.Where("name = ?", name).First(&ch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("channel %q: %w", name, ErrNotFound)
			}
			return err
		}
		var tokenCount int64
		if err := tx.Model(&IngestToken{}).Where("channel_id = ?", ch.ID).Count(&tokenCount).Error; err != nil {
			return err
		}
		if tokenCount > 0 {
			return fmt.Errorf("channel %q has %d token(s): %w", name, tokenCount, ErrChannelInUse)
		}
		return tx.Delete(&ch).Error
	})
}

// DeleteChannelsForRoom removes every channel bound to a room together with
// their tokens, in one transaction. Used when the bot leaves a room: the
// mappings are dead weight after that. Returns the names of the deleted
// channels.
func (s *Store) DeleteChannelsForRoom(ctx context.Context, roomID string) ([]string, error) {
	var deleted []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var channels []Channel
		if err := tx.Where("room_id = ?", roomID).Find(&channels).Error; err != nil {
			return err
		}
		for _, ch := range channels {
			if err := tx.Where("channel_id = ?", ch.ID).Delete(&IngestToken{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&ch).Error; err != nil {
				return err
			}
			deleted = append(deleted, ch.Name)
		}
		return nil
	})
	return deleted, err
}

// UpdateChannelChart toggles chart rendering for a channel.
func (s *Store) UpdateChannelChart(ctx context.Context, name string, chart bool) (*Channel, error) {
	res := s.db.WithContext(ctx).Model(&Channel{}).Where("name = ?", name).Update("chart", chart)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("channel %q: %w", name, ErrNotFound)
	}
	return s.GetChannel(ctx, name)
}

// UpdateChannelRoomID rewrites a channel's room, e.g. when a stored alias is
// resolved to its room ID.
func (s *Store) UpdateChannelRoomID(ctx context.Context, name, roomID string) error {
	res := s.db.WithContext(ctx).Model(&Channel{}).Where("name = ?", name).Update("room_id", roomID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("channel %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	var chs []Channel
	return chs, s.db.WithContext(ctx).Order("name").Find(&chs).Error
}

func (s *Store) GetChannel(ctx context.Context, name string) (*Channel, error) {
	var ch Channel
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&ch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("channel %q: %w", name, ErrNotFound)
		}
		return nil, err
	}
	return &ch, nil
}

// CreateToken mints a new random ingest token for a channel and returns the
// plaintext exactly once; only its hash is stored.
func (s *Store) CreateToken(ctx context.Context, name string, kind TokenKind, channelName string) (string, *IngestToken, error) {
	ch, err := s.GetChannel(ctx, channelName)
	if err != nil {
		return "", nil, err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generating token: %w", err)
	}
	plaintext := "mn_" + hex.EncodeToString(raw)
	tok := IngestToken{
		Name:      name,
		Kind:      kind,
		TokenHash: HashToken(plaintext),
		ChannelID: ch.ID,
		Channel:   *ch,
	}
	if err := s.db.WithContext(ctx).Create(&tok).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueViolation(err) {
			return "", nil, fmt.Errorf("token %q: %w", name, ErrAlreadyExists)
		}
		return "", nil, err
	}
	tok.Channel = *ch
	return plaintext, &tok, nil
}

func (s *Store) DeleteToken(ctx context.Context, name string) error {
	res := s.db.WithContext(ctx).Where("name = ?", name).Delete(&IngestToken{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("token %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Store) ListTokens(ctx context.Context) ([]IngestToken, error) {
	var toks []IngestToken
	return toks, s.db.WithContext(ctx).Preload("Channel").Order("name").Find(&toks).Error
}

// ResolveToken authenticates a presented ingest token for the given endpoint
// kind and returns the channel it routes to. LastUsedAt is updated
// best-effort.
func (s *Store) ResolveToken(ctx context.Context, plaintext string, endpoint TokenKind) (*Channel, error) {
	var tok IngestToken
	err := s.db.WithContext(ctx).Preload("Channel").
		Where("token_hash = ?", HashToken(plaintext)).First(&tok).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if tok.Kind != KindAny && tok.Kind != endpoint {
		return nil, ErrNotFound
	}
	now := time.Now()
	s.db.WithContext(ctx).Model(&IngestToken{}).Where("id = ?", tok.ID).Update("last_used_at", now)
	return &tok.Channel, nil
}

// HashToken is the storage form of ingest tokens: they are high-entropy
// random values (not passwords), so a fast hash with an indexed lookup is
// appropriate; argon2 is reserved for the admin token.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// isUniqueViolation catches driver-specific unique constraint errors that
// GORM does not translate on every dialect.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "duplicate key value")
}
