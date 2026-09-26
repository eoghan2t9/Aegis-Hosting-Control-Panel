package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SecretBox encrypts and decrypts small secrets kept in the database. It is
// implemented by svc.Cipher (AES-256-GCM); the store only sees this interface
// because svc imports store, not the other way round.
type SecretBox interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

// encPrefix marks a stored value as encrypted at rest. A value without it is
// legacy plaintext: still readable, and rewritten by EncryptLegacySecrets.
const encPrefix = "enc:v1:"

// SetSecretBox configures the box used to seal secret columns (currently
// databases.db_password). With none set the store keeps the old plaintext
// behaviour, which only tests and tools that never touch secrets rely on.
func (s *Store) SetSecretBox(b SecretBox) { s.box = b }

// sealSecret returns the form of plain that is written to the database.
// Empty values are left empty so "no password" stays distinguishable.
func (s *Store) sealSecret(plain string) (string, error) {
	if s.box == nil || plain == "" {
		return plain, nil
	}
	enc, err := s.box.Encrypt(plain)
	if err != nil {
		return "", fmt.Errorf("encrypt secret: %w", err)
	}
	return encPrefix + enc, nil
}

// openSecret reverses sealSecret. Legacy plaintext is returned unchanged; an
// encrypted value with no box, or one that fails to decrypt (wrong panel
// secret), is an error rather than silently handing back ciphertext, which a
// caller would then use as a real password.
func (s *Store) openSecret(stored string) (string, error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil
	}
	if s.box == nil {
		return "", errors.New("store: secret is encrypted but no secret box is configured")
	}
	plain, err := s.box.Decrypt(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return plain, nil
}

// EncryptLegacySecrets encrypts every plaintext databases.db_password in one
// transaction and returns how many rows it changed. It is idempotent: rows
// already carrying the encPrefix, and empty passwords, are skipped.
func (s *Store) EncryptLegacySecrets(ctx context.Context) (int, error) {
	if s.box == nil {
		return 0, errors.New("store: no secret box configured")
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, db_password FROM databases WHERE db_password <> '' AND substr(db_password, 1, ?) <> ?",
		len(encPrefix), encPrefix)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id    int64
		plain string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.plain); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(todo) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, p := range todo {
		enc, err := s.sealSecret(p.plain)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE databases SET db_password = ? WHERE id = ?", enc, p.id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(todo), nil
}
