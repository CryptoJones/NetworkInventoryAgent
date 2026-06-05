package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// HostRepo is the SQLite implementation of store.HostStore. It uses the
// shared writer pool for mutations (Upsert/Delete) and the reader pool for
// queries (GetByIP/List/Count), so dashboard reads no longer block behind
// the scanner's upserts.
type HostRepo struct {
	writer *sql.DB
	reader *sql.DB
}

// compile-time interface check
var _ store.HostStore = (*HostRepo)(nil)

func (r *HostRepo) Upsert(ctx context.Context, h *models.Host) (int64, error) {
	const q = `
		INSERT INTO hosts (ip_address, mac_address, hostname, os_fingerprint, vendor, device_type)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(ip_address) DO UPDATE SET
			mac_address    = excluded.mac_address,
			hostname       = excluded.hostname,
			os_fingerprint = excluded.os_fingerprint,
			vendor         = excluded.vendor,
			device_type    = excluded.device_type,
			last_seen      = CURRENT_TIMESTAMP
		RETURNING id`

	var id int64
	err := r.writer.QueryRowContext(ctx, q,
		h.IPAddress, h.MACAddress, h.Hostname,
		h.OSFingerprint, h.Vendor, h.DeviceType,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert host %s: %w", h.IPAddress, err)
	}
	return id, nil
}

func (r *HostRepo) GetByIP(ctx context.Context, ip string) (*models.Host, error) {
	const q = `
		SELECT id, ip_address, mac_address, hostname, os_fingerprint,
		       vendor, device_type, first_seen, last_seen
		FROM hosts WHERE ip_address = ?`

	h := &models.Host{}
	err := r.reader.QueryRowContext(ctx, q, ip).Scan(
		&h.ID, &h.IPAddress, &h.MACAddress, &h.Hostname, &h.OSFingerprint,
		&h.Vendor, &h.DeviceType, &h.FirstSeen, &h.LastSeen,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get host by ip %s: %w", ip, err)
	}
	return h, nil
}

func (r *HostRepo) List(ctx context.Context) ([]*models.Host, error) {
	const q = `
		SELECT id, ip_address, mac_address, hostname, os_fingerprint,
		       vendor, device_type, first_seen, last_seen
		FROM hosts ORDER BY ip_address`

	rows, err := r.reader.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hosts []*models.Host
	for rows.Next() {
		h := &models.Host{}
		if err := rows.Scan(
			&h.ID, &h.IPAddress, &h.MACAddress, &h.Hostname, &h.OSFingerprint,
			&h.Vendor, &h.DeviceType, &h.FirstSeen, &h.LastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan host row: %w", err)
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

func (r *HostRepo) ListPage(ctx context.Context, limit, offset int) ([]*models.Host, error) {
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
		SELECT id, ip_address, mac_address, hostname, os_fingerprint,
		       vendor, device_type, first_seen, last_seen
		FROM hosts ORDER BY ip_address LIMIT ? OFFSET ?`

	rows, err := r.reader.QueryContext(ctx, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list hosts page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hosts []*models.Host
	for rows.Next() {
		h := &models.Host{}
		if err := rows.Scan(
			&h.ID, &h.IPAddress, &h.MACAddress, &h.Hostname, &h.OSFingerprint,
			&h.Vendor, &h.DeviceType, &h.FirstSeen, &h.LastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan host row: %w", err)
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

func (r *HostRepo) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.reader.QueryRowContext(ctx, `SELECT count(*) FROM hosts`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count hosts: %w", err)
	}
	return n, nil
}

func (r *HostRepo) Delete(ctx context.Context, id int64) error {
	if _, err := r.writer.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete host %d: %w", id, err)
	}
	return nil
}
