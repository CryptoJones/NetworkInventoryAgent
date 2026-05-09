// Package store defines the persistence interfaces for the inventory agent.
// All database implementations must satisfy these interfaces so that the
// backing store can be swapped without changing any business logic.
package store

import (
	"context"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/models"
)

type HostStore interface {
	Upsert(ctx context.Context, h *models.Host) (int64, error)
	GetByIP(ctx context.Context, ip string) (*models.Host, error)
	List(ctx context.Context) ([]*models.Host, error)
	Delete(ctx context.Context, id int64) error
}

type PortStore interface {
	Upsert(ctx context.Context, p *models.Port) error
	ListByHost(ctx context.Context, hostID int64) ([]*models.Port, error)
	DeleteByHost(ctx context.Context, hostID int64) error
}

type ScanStore interface {
	Create(ctx context.Context, s *models.Scan) (int64, error)
	// Finish marks a scan as complete, updating hosts_found and finished_at.
	Finish(ctx context.Context, id int64, hostsFound int, finishedAt time.Time) error
	List(ctx context.Context) ([]*models.Scan, error)
}
