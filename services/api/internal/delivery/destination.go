package delivery

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresDestinationReader decrypts a verified provider target only for the
// immediate Send call. Plaintext targets are never returned by the API or
// persisted in messages.
type PostgresDestinationReader struct {
	pool *pgxpool.Pool
	key  []byte
}

func NewPostgresDestinationReader(pool *pgxpool.Pool, key []byte) (*PostgresDestinationReader, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: destination key must be 32 bytes", domain.ErrInvalidArgument)
	}
	copyKey := append([]byte(nil), key...)
	return &PostgresDestinationReader{pool: pool, key: copyKey}, nil
}

func (r *PostgresDestinationReader) ResolveVerifiedDestination(ctx context.Context, accountID string, channel Channel, reference string) (VerifiedDestination, error) {
	var ciphertext []byte
	err := r.pool.QueryRow(ctx, `SELECT provider_target_ciphertext FROM account_destinations WHERE account_id IN (SELECT account_id FROM lingow_account_lineage($1)) AND channel=$2 AND destination_ref=$3 AND verified_at IS NOT NULL AND revoked_at IS NULL ORDER BY (account_id=$1) DESC,updated_at DESC LIMIT 1`, accountID, channel, reference).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return VerifiedDestination{}, domain.ErrNotFound
	}
	if err != nil {
		return VerifiedDestination{}, err
	}
	target, err := decryptTarget(r.key, ciphertext)
	if err != nil {
		return VerifiedDestination{}, domain.ErrUnauthorized
	}
	return VerifiedDestination{AccountID: accountID, Channel: channel, DestinationRef: reference, ProviderTarget: target}, nil
}

// EncryptProviderTarget is used by a trusted provisioning command, never by a
// public request handler. It returns the value suitable for account_destinations.
func EncryptProviderTarget(key []byte, target string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(target), nil), nil
}

func decryptTarget(key, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("invalid destination ciphertext")
	}
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func DecodeDestinationKey(encoded string) ([]byte, error) {
	var lastErr error
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		key, err := encoding.DecodeString(encoded)
		if err != nil {
			lastErr = err
			continue
		}
		if len(key) == 32 {
			return key, nil
		}
		lastErr = fmt.Errorf("destination key must decode to 32 bytes")
	}
	return nil, lastErr
}

var _ DestinationReader = (*PostgresDestinationReader)(nil)
