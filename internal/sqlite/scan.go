package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// ScanRepo is the SQLite implementation of store.ScanStore.
type ScanRepo struct {
	conn *sql.DB
}

var _ store.ScanStore = (*ScanRepo)(nil)

func (r *ScanRepo) Create(ctx context.Context, s *models.Scan) (int64, error) {
	const q = `
		INSERT INTO scans (subnet, hosts_found, started_at, finished_at)
		VALUES (?, ?, ?, ?) RETURNING id`

	var id int64
	if err := r.conn.QueryRowContext(ctx, q,
		s.Subnet, s.HostsFound, s.StartedAt, s.FinishedAt,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("create scan: %w", err)
	}
	return id, nil
}

func (r *ScanRepo) Finish(ctx context.Context, id int64, hostsFound int, finishedAt time.Time) error {
	const q = `UPDATE scans SET hosts_found = ?, finished_at = ? WHERE id = ?`
	_, err := r.conn.ExecContext(ctx, q, hostsFound, finishedAt, id)
	if err != nil {
		return fmt.Errorf("finish scan %d: %w", id, err)
	}
	return nil
}

func (r *ScanRepo) List(ctx context.Context) ([]*models.Scan, error) {
	const q = `
		SELECT id, subnet, hosts_found, started_at, finished_at
		FROM scans ORDER BY started_at DESC`

	rows, err := r.conn.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}
	defer rows.Close()

	var scans []*models.Scan
	for rows.Next() {
		s := &models.Scan{}
		var finishedAt sql.NullTime
		if err := rows.Scan(
			&s.ID, &s.Subnet, &s.HostsFound, &s.StartedAt, &finishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan scan row: %w", err)
		}
		if finishedAt.Valid {
			s.FinishedAt = &finishedAt.Time
		}
		scans = append(scans, s)
	}
	return scans, rows.Err()
}
