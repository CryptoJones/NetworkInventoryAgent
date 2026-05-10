// Package store defines the persistence interfaces for the inventory agent.
// All database implementations must satisfy these interfaces so that the
// backing store can be swapped without changing any business logic.
package store

import (
	"context"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// HostStore persists and retrieves network hosts.
type HostStore interface {
	// Upsert inserts h if its IP address is new, or updates all mutable fields
	// if the IP already exists. Returns the host's database ID.
	Upsert(ctx context.Context, h *models.Host) (int64, error)

	// GetByIP returns the host with the given IP address, or ErrNotFound if no
	// such host exists.
	GetByIP(ctx context.Context, ip string) (*models.Host, error)

	// List returns all hosts ordered by IP address.
	List(ctx context.Context) ([]*models.Host, error)

	// Count returns the total number of hosts in the inventory.
	Count(ctx context.Context) (int, error)

	// Delete removes the host with the given ID and cascades to its ports.
	Delete(ctx context.Context, id int64) error
}

// PortStore persists and retrieves ports associated with a host.
type PortStore interface {
	// Upsert inserts p if the (host_id, port, protocol) tuple is new, or updates
	// service and state if it already exists.
	Upsert(ctx context.Context, p *models.Port) error

	// ListByHost returns all ports for the given host ordered by number then protocol.
	ListByHost(ctx context.Context, hostID int64) ([]*models.Port, error)

	// DeleteByHost removes all ports for the given host.
	DeleteByHost(ctx context.Context, hostID int64) error
}

// ScanStore persists and retrieves subnet scan records.
type ScanStore interface {
	// Create inserts a new scan record and returns its database ID. FinishedAt
	// may be nil if the scan is still in progress.
	Create(ctx context.Context, s *models.Scan) (int64, error)

	// Finish marks a scan as complete, updating hosts_found and finished_at.
	// Returns an error if no record with the given ID exists.
	Finish(ctx context.Context, id int64, hostsFound int, finishedAt time.Time) error

	// List returns all scans ordered by started_at descending (newest first).
	List(ctx context.Context) ([]*models.Scan, error)
}
