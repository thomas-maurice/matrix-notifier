package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AdminCredential is the single-row table holding the admin password hash
// and the secret JWT sessions are signed with. The config's admin_token_hash
// only seeds this row on first start; afterwards the database is the source
// of truth and the password is changed via ChangeAdminPassword.
type AdminCredential struct {
	ID        uint      `gorm:"primarykey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	// Argon2id PHC string of the admin password.
	PasswordHash string `gorm:"not null"`
	// HMAC key for session JWTs; rotated on every password change so a
	// rotation revokes all outstanding sessions at once.
	JWTSecret []byte `gorm:"not null"`
}

func newJWTSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generating jwt secret: %w", err)
	}
	return secret, nil
}

// SeedAdminCredential returns the stored credential, creating it from the
// given argon2id hash (with a fresh JWT secret) when none exists yet.
func (s *Store) SeedAdminCredential(ctx context.Context, passwordHash string) (*AdminCredential, error) {
	var cred AdminCredential
	err := s.db.WithContext(ctx).First(&cred).Error
	if err == nil {
		return &cred, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	secret, err := newJWTSecret()
	if err != nil {
		return nil, err
	}
	cred = AdminCredential{PasswordHash: passwordHash, JWTSecret: secret}
	if err := s.db.WithContext(ctx).Create(&cred).Error; err != nil {
		return nil, fmt.Errorf("seeding admin credential: %w", err)
	}
	return &cred, nil
}

// UpdateAdminPassword stores a new password hash and rotates the JWT secret,
// which invalidates every session signed with the old one.
func (s *Store) UpdateAdminPassword(ctx context.Context, passwordHash string) (*AdminCredential, error) {
	secret, err := newJWTSecret()
	if err != nil {
		return nil, err
	}
	var cred AdminCredential
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&cred).Error; err != nil {
			return err
		}
		cred.PasswordHash = passwordHash
		cred.JWTSecret = secret
		return tx.Save(&cred).Error
	})
	if err != nil {
		return nil, fmt.Errorf("updating admin credential: %w", err)
	}
	return &cred, nil
}
