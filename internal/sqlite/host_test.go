package sqlite_test

import (
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHost returns a Host with sensible defaults for a given IP.
func newTestHost(ip string) *models.Host {
	return &models.Host{
		IPAddress:     ip,
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Hostname:      "test-host",
		OSFingerprint: "Linux 5.x",
		Vendor:        "Acme",
		DeviceType:    "server",
	}
}

func TestHostRepo_Upsert_Insert(t *testing.T) {
	hosts := openTestDB(t).Hosts()

	id, err := hosts.Upsert(t.Context(), newTestHost("10.0.0.1"))
	require.NoError(t, err)
	assert.Greater(t, id, int64(0), "inserted host should have a positive ID")
}

func TestHostRepo_Upsert_ReturnsConsistentID(t *testing.T) {
	hosts := openTestDB(t).Hosts()
	h := newTestHost("10.0.0.1")

	id1, err := hosts.Upsert(t.Context(), h)
	require.NoError(t, err)

	id2, err := hosts.Upsert(t.Context(), h)
	require.NoError(t, err)

	assert.Equal(t, id1, id2, "upserting the same host should return the same ID")
}

func TestHostRepo_Upsert_UpdatesFields(t *testing.T) {
	hosts := openTestDB(t).Hosts()
	ctx := t.Context()

	original := newTestHost("10.0.0.1")
	_, err := hosts.Upsert(ctx, original)
	require.NoError(t, err)

	updated := newTestHost("10.0.0.1")
	updated.Hostname = "updated-hostname"
	updated.OSFingerprint = "Windows 11"
	_, err = hosts.Upsert(ctx, updated)
	require.NoError(t, err)

	got, err := hosts.GetByIP(ctx, "10.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, "updated-hostname", got.Hostname)
	assert.Equal(t, "Windows 11", got.OSFingerprint)
}

func TestHostRepo_GetByIP_Found(t *testing.T) {
	hosts := openTestDB(t).Hosts()
	ctx := t.Context()

	h := newTestHost("10.0.0.2")
	_, err := hosts.Upsert(ctx, h)
	require.NoError(t, err)

	got, err := hosts.GetByIP(ctx, "10.0.0.2")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.2", got.IPAddress)
	assert.Equal(t, h.MACAddress, got.MACAddress)
	assert.Equal(t, h.Hostname, got.Hostname)
	assert.Greater(t, got.ID, int64(0))
}

func TestHostRepo_GetByIP_NotFound(t *testing.T) {
	hosts := openTestDB(t).Hosts()

	_, err := hosts.GetByIP(t.Context(), "1.2.3.4")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestHostRepo_List_Empty(t *testing.T) {
	hosts := openTestDB(t).Hosts()

	list, err := hosts.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestHostRepo_List_OrderedByIP(t *testing.T) {
	hosts := openTestDB(t).Hosts()
	ctx := t.Context()

	for _, ip := range []string{"10.0.0.3", "10.0.0.1", "10.0.0.2"} {
		_, err := hosts.Upsert(ctx, newTestHost(ip))
		require.NoError(t, err)
	}

	list, err := hosts.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "10.0.0.1", list[0].IPAddress)
	assert.Equal(t, "10.0.0.2", list[1].IPAddress)
	assert.Equal(t, "10.0.0.3", list[2].IPAddress)
}

func TestHostRepo_Count(t *testing.T) {
	hosts := openTestDB(t).Hosts()
	ctx := t.Context()

	n, err := hosts.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "empty table should report zero hosts")

	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		_, err := hosts.Upsert(ctx, newTestHost(ip))
		require.NoError(t, err)
	}

	n, err = hosts.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestHostRepo_Delete(t *testing.T) {
	hosts := openTestDB(t).Hosts()
	ctx := t.Context()

	id, err := hosts.Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	require.NoError(t, hosts.Delete(ctx, id))

	_, err = hosts.GetByIP(ctx, "10.0.0.1")
	assert.ErrorIs(t, err, store.ErrNotFound, "deleted host should no longer be found")
}

func TestHostRepo_Delete_CascadesToPorts(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	hostID, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	err = db.Ports().Upsert(ctx, &models.Port{
		HostID: hostID, Number: 80, Protocol: models.TCP, Service: "http", State: models.StateOpen,
	})
	require.NoError(t, err)

	require.NoError(t, db.Hosts().Delete(ctx, hostID))

	ports, err := db.Ports().ListByHost(ctx, hostID)
	require.NoError(t, err)
	assert.Empty(t, ports, "ports should be cascade-deleted with their host")
}

func TestHostRepo_ListPage(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		_, err := db.Hosts().Upsert(ctx, newTestHost(ip))
		require.NoError(t, err)
	}

	n, err := db.Hosts().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	// Ordered by IP: page 1 of size 2.
	page, err := db.Hosts().ListPage(ctx, 2, 0)
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, "10.0.0.1", page[0].IPAddress)
	assert.Equal(t, "10.0.0.2", page[1].IPAddress)

	// Page 2 has the remainder.
	page, err = db.Hosts().ListPage(ctx, 2, 2)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, "10.0.0.3", page[0].IPAddress)

	// limit<=0 means no limit.
	page, err = db.Hosts().ListPage(ctx, 0, 0)
	require.NoError(t, err)
	assert.Len(t, page, 3)
}
