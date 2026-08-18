package devices

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepository makes pairing and challenge consumption transactional so
// concurrent attempts cannot bind a device twice or replay a nonce.
type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Provision(ctx context.Context, device Device) (Device, error) {
	if r == nil || r.pool == nil || device.DeviceID == "" || device.ProductID == "" || len(device.PublicKey) != 32 || !device.Status.valid() || device.CreatedAt.IsZero() {
		return Device{}, domain.ErrInvalidArgument
	}
	stored, err := scanDevice(r.pool.QueryRow(ctx, `
		INSERT INTO lingow_devices (device_id, product_id, public_key, status, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (device_id) DO UPDATE SET device_id = EXCLUDED.device_id
		WHERE lingow_devices.product_id = EXCLUDED.product_id
		  AND lingow_devices.public_key = EXCLUDED.public_key
		RETURNING device_id, product_id, public_key, account_id, status, bound_at, created_at`,
		device.DeviceID, device.ProductID, device.PublicKey, device.Status, device.CreatedAt.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrConflict
	}
	return stored, deviceError(err)
}

func (r *PostgresRepository) GetActive(ctx context.Context, deviceID string) (Device, error) {
	if r == nil || r.pool == nil || deviceID == "" {
		return Device{}, domain.ErrInvalidArgument
	}
	device, err := scanDevice(r.pool.QueryRow(ctx, `
		SELECT device_id, product_id, public_key, account_id, status, bound_at, created_at
		FROM lingow_devices WHERE device_id=$1 AND status='active'`, deviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrUnauthorized
	}
	return device, deviceError(err)
}

func (r *PostgresRepository) CanCreatePairingCode(ctx context.Context, accountID string) error {
	if r == nil || r.pool == nil || accountID == "" {
		return domain.ErrUnauthorized
	}
	var found string
	err := r.pool.QueryRow(ctx, `SELECT id FROM lingow_accounts WHERE id=$1 AND kind='registered' AND merged_into IS NULL`, accountID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrForbidden
	}
	return deviceError(err)
}

func (r *PostgresRepository) ListBound(ctx context.Context, accountID string) ([]Device, error) {
	if r == nil || r.pool == nil || accountID == "" {
		return nil, domain.ErrUnauthorized
	}
	rows, err := r.pool.Query(ctx, `
		SELECT device_id, product_id, public_key, account_id, status, bound_at, created_at
		FROM lingow_devices WHERE account_id=$1 ORDER BY created_at DESC, device_id DESC`, accountID)
	if err != nil {
		return nil, deviceError(err)
	}
	defer rows.Close()
	items := make([]Device, 0)
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, deviceError(err)
		}
		items = append(items, device)
	}
	if err := rows.Err(); err != nil {
		return nil, deviceError(err)
	}
	return items, nil
}

func (r *PostgresRepository) Revoke(ctx context.Context, accountID, deviceID string) error {
	if r == nil || r.pool == nil || accountID == "" || deviceID == "" {
		return domain.ErrInvalidArgument
	}
	var found string
	err := r.pool.QueryRow(ctx, `
		UPDATE lingow_devices
		SET status='revoked', revoked_at=clock_timestamp()
		WHERE device_id=$1 AND account_id=$2 AND status='active'
		RETURNING device_id`, deviceID, accountID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return deviceError(err)
}

func (r *PostgresRepository) CreatePairingCode(ctx context.Context, pairing PairingCode) error {
	if r == nil || r.pool == nil || pairing.ID == "" || pairing.AccountID == "" || len(pairing.Hash) != 32 || !pairing.ExpiresAt.After(pairing.CreatedAt) {
		return domain.ErrInvalidArgument
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO lingow_device_pairing_codes (id, account_id, code_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`, pairing.ID, pairing.AccountID, pairing.Hash, pairing.ExpiresAt.UTC(), pairing.CreatedAt.UTC())
	return deviceError(err)
}

func (r *PostgresRepository) BindWithPairingCode(ctx context.Context, deviceID string, codeHash []byte) (Device, error) {
	if r == nil || r.pool == nil || deviceID == "" || len(codeHash) != 32 {
		return Device{}, domain.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Device{}, deviceError(err)
	}
	defer tx.Rollback(ctx)
	device, err := scanDevice(tx.QueryRow(ctx, `SELECT device_id, product_id, public_key, account_id, status, bound_at, created_at FROM lingow_devices WHERE device_id=$1 FOR UPDATE`, deviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrUnauthorized
	}
	if err != nil {
		return Device{}, deviceError(err)
	}
	if device.Status != StatusActive {
		return Device{}, domain.ErrUnauthorized
	}
	if device.AccountID != nil {
		return Device{}, domain.ErrConflict
	}
	var id, accountID string
	var storedHash []byte
	err = tx.QueryRow(ctx, `
		SELECT id, account_id, code_hash FROM lingow_device_pairing_codes
		WHERE code_hash=$1 AND used_at IS NULL AND expires_at > clock_timestamp()
		FOR UPDATE`, codeHash).Scan(&id, &accountID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrUnauthorized
	}
	if err != nil {
		return Device{}, deviceError(err)
	}
	if subtle.ConstantTimeCompare(storedHash, codeHash) != 1 {
		return Device{}, domain.ErrUnauthorized
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE lingow_device_pairing_codes SET used_at=$2 WHERE id=$1 AND used_at IS NULL`, id, now); err != nil {
		return Device{}, deviceError(err)
	}
	device, err = scanDevice(tx.QueryRow(ctx, `
		UPDATE lingow_devices SET account_id=$2, bound_at=$3 WHERE device_id=$1 AND account_id IS NULL AND status='active'
		RETURNING device_id, product_id, public_key, account_id, status, bound_at, created_at`, deviceID, accountID, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrConflict
	}
	if err != nil {
		return Device{}, deviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, deviceError(err)
	}
	return device, nil
}

func (r *PostgresRepository) CreateChallenge(ctx context.Context, challenge Challenge) error {
	if r == nil || r.pool == nil || challenge.ID == "" || challenge.DeviceID == "" || challenge.Nonce == "" || !challenge.ExpiresAt.After(challenge.CreatedAt) {
		return domain.ErrInvalidArgument
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO lingow_device_auth_challenges (id, device_id, nonce, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`, challenge.ID, challenge.DeviceID, challenge.Nonce, challenge.ExpiresAt.UTC(), challenge.CreatedAt.UTC())
	return deviceError(err)
}

func (r *PostgresRepository) GetChallenge(ctx context.Context, challengeID, deviceID string) (Challenge, error) {
	if r == nil || r.pool == nil || challengeID == "" || deviceID == "" {
		return Challenge{}, domain.ErrInvalidArgument
	}
	var value Challenge
	err := r.pool.QueryRow(ctx, `SELECT id, device_id, nonce, expires_at, created_at FROM lingow_device_auth_challenges WHERE id=$1 AND device_id=$2 AND used_at IS NULL AND expires_at > clock_timestamp()`, challengeID, deviceID).Scan(&value.ID, &value.DeviceID, &value.Nonce, &value.ExpiresAt, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, domain.ErrUnauthorized
	}
	return value, deviceError(err)
}

func (r *PostgresRepository) ConsumeChallenge(ctx context.Context, challengeID, deviceID string) (Device, error) {
	if r == nil || r.pool == nil || challengeID == "" || deviceID == "" {
		return Device{}, domain.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Device{}, deviceError(err)
	}
	defer tx.Rollback(ctx)
	var found string
	err = tx.QueryRow(ctx, `UPDATE lingow_device_auth_challenges SET used_at=clock_timestamp() WHERE id=$1 AND device_id=$2 AND used_at IS NULL AND expires_at > clock_timestamp() RETURNING id`, challengeID, deviceID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrUnauthorized
	}
	if err != nil {
		return Device{}, deviceError(err)
	}
	device, err := scanDevice(tx.QueryRow(ctx, `SELECT device_id, product_id, public_key, account_id, status, bound_at, created_at FROM lingow_devices WHERE device_id=$1 AND status='active'`, deviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, domain.ErrUnauthorized
	}
	if err != nil {
		return Device{}, deviceError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lingow_devices SET last_seen_at=clock_timestamp() WHERE device_id=$1`, deviceID); err != nil {
		return Device{}, deviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, deviceError(err)
	}
	return device, nil
}

func (r *PostgresRepository) OwnsSession(ctx context.Context, deviceID, accountID, sessionID string) error {
	if r == nil || r.pool == nil || deviceID == "" || accountID == "" || sessionID == "" {
		return domain.ErrUnauthorized
	}
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM lingow_device_voice_sessions WHERE device_id=$1 AND account_id=$2 AND session_id=$3)`, deviceID, accountID, sessionID).Scan(&exists)
	if err != nil {
		return deviceError(err)
	}
	if !exists {
		return domain.ErrUnauthorized
	}
	return nil
}

func (r *PostgresRepository) BindSession(ctx context.Context, deviceID, accountID, sessionID string) error {
	if r == nil || r.pool == nil || deviceID == "" || accountID == "" || sessionID == "" {
		return domain.ErrInvalidArgument
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO lingow_device_voice_sessions (device_id, session_id, account_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, deviceID, sessionID, accountID)
	return deviceError(err)
}

func (r *PostgresRepository) ActiveBound(ctx context.Context, deviceID, accountID string) error {
	var found string
	err := r.pool.QueryRow(ctx, `SELECT device_id FROM lingow_devices WHERE device_id=$1 AND account_id=$2 AND status='active'`, deviceID, accountID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrUnauthorized
	}
	return deviceError(err)
}

func scanDevice(row pgx.Row) (Device, error) {
	var device Device
	var accountID *string
	var status string
	err := row.Scan(&device.DeviceID, &device.ProductID, &device.PublicKey, &accountID, &status, &device.BoundAt, &device.CreatedAt)
	if err != nil {
		return Device{}, err
	}
	device.AccountID, device.Status = accountID, Status(status)
	if !device.Status.valid() || len(device.PublicKey) != 32 {
		return Device{}, fmt.Errorf("invalid persisted device")
	}
	return device, nil
}

func deviceError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

var _ Repository = (*PostgresRepository)(nil)
