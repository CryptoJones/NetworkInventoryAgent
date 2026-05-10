package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// PortRepo is the SQLite implementation of store.PortStore.
type PortRepo struct {
	conn *sql.DB
}

var _ store.PortStore = (*PortRepo)(nil)

func (r *PortRepo) Upsert(ctx context.Context, p *models.Port) error {
	const q = `
		INSERT INTO ports (host_id, port, protocol, service, state)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(host_id, port, protocol) DO UPDATE SET
			service   = excluded.service,
			state     = excluded.state,
			last_seen = CURRENT_TIMESTAMP`

	if _, err := r.conn.ExecContext(ctx, q,
		p.HostID, p.Number, string(p.Protocol), p.Service, string(p.State),
	); err != nil {
		return fmt.Errorf("upsert port %d/%s on host %d: %w", p.Number, p.Protocol, p.HostID, err)
	}
	return nil
}

func (r *PortRepo) ListByHost(ctx context.Context, hostID int64) ([]*models.Port, error) {
	const q = `
		SELECT id, host_id, port, protocol, service, state, first_seen, last_seen
		FROM ports WHERE host_id = ? ORDER BY port, protocol`

	rows, err := r.conn.QueryContext(ctx, q, hostID)
	if err != nil {
		return nil, fmt.Errorf("list ports for host %d: %w", hostID, err)
	}
	defer rows.Close()

	var ports []*models.Port
	for rows.Next() {
		p := &models.Port{}
		var protocol, state string
		if err := rows.Scan(
			&p.ID, &p.HostID, &p.Number, &protocol,
			&p.Service, &state, &p.FirstSeen, &p.LastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan port row: %w", err)
		}
		p.Protocol = models.Protocol(protocol)
		p.State = models.PortState(state)
		ports = append(ports, p)
	}
	return ports, rows.Err()
}

func (r *PortRepo) DeleteByHost(ctx context.Context, hostID int64) error {
	if _, err := r.conn.ExecContext(ctx,
		`DELETE FROM ports WHERE host_id = ?`, hostID,
	); err != nil {
		return fmt.Errorf("delete ports for host %d: %w", hostID, err)
	}
	return nil
}
