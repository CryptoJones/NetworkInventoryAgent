package sqlite_test

import (
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPort(hostID int64, number int, protocol models.Protocol) *models.Port {
	return &models.Port{
		HostID:   hostID,
		Number:   number,
		Protocol: protocol,
		Service:  "unknown",
		State:    models.StateOpen,
	}
}

func TestPortRepo_Upsert_Insert(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	hostID, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	err = db.Ports().Upsert(ctx, newTestPort(hostID, 22, models.TCP))
	require.NoError(t, err)
}

func TestPortRepo_Upsert_UpdatesState(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	hostID, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	p := newTestPort(hostID, 443, models.TCP)
	p.Service = "https"
	p.State = models.StateOpen
	require.NoError(t, db.Ports().Upsert(ctx, p))

	p.State = models.StateFiltered
	p.Service = "https-filtered"
	require.NoError(t, db.Ports().Upsert(ctx, p))

	ports, err := db.Ports().ListByHost(ctx, hostID)
	require.NoError(t, err)
	require.Len(t, ports, 1, "upsert should not create a duplicate")
	assert.Equal(t, models.StateFiltered, ports[0].State)
	assert.Equal(t, "https-filtered", ports[0].Service)
}

func TestPortRepo_ListByHost_Empty(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	hostID, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	ports, err := db.Ports().ListByHost(ctx, hostID)
	require.NoError(t, err)
	assert.Empty(t, ports)
}

func TestPortRepo_ListByHost_OrderedByPortAndProtocol(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	hostID, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	for _, p := range []*models.Port{
		{HostID: hostID, Number: 443, Protocol: models.TCP, State: models.StateOpen},
		{HostID: hostID, Number: 80, Protocol: models.UDP, State: models.StateOpen},
		{HostID: hostID, Number: 80, Protocol: models.TCP, State: models.StateOpen},
	} {
		require.NoError(t, db.Ports().Upsert(ctx, p))
	}

	ports, err := db.Ports().ListByHost(ctx, hostID)
	require.NoError(t, err)
	require.Len(t, ports, 3)
	assert.Equal(t, 80, ports[0].Number)
	assert.Equal(t, models.TCP, ports[0].Protocol)
	assert.Equal(t, 80, ports[1].Number)
	assert.Equal(t, models.UDP, ports[1].Protocol)
	assert.Equal(t, 443, ports[2].Number)
}

func TestPortRepo_ListByHost_IsolatedPerHost(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	id1, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)
	id2, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.2"))
	require.NoError(t, err)

	require.NoError(t, db.Ports().Upsert(ctx, newTestPort(id1, 22, models.TCP)))
	require.NoError(t, db.Ports().Upsert(ctx, newTestPort(id2, 80, models.TCP)))

	ports, err := db.Ports().ListByHost(ctx, id1)
	require.NoError(t, err)
	require.Len(t, ports, 1)
	assert.Equal(t, 22, ports[0].Number)
}

func TestPortRepo_DeleteByHost(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	hostID, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)

	for _, port := range []int{22, 80, 443} {
		require.NoError(t, db.Ports().Upsert(ctx, newTestPort(hostID, port, models.TCP)))
	}

	require.NoError(t, db.Ports().DeleteByHost(ctx, hostID))

	ports, err := db.Ports().ListByHost(ctx, hostID)
	require.NoError(t, err)
	assert.Empty(t, ports)
}

func TestPortRepo_DeleteByHost_DoesNotAffectOtherHosts(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	id1, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.1"))
	require.NoError(t, err)
	id2, err := db.Hosts().Upsert(ctx, newTestHost("10.0.0.2"))
	require.NoError(t, err)

	require.NoError(t, db.Ports().Upsert(ctx, newTestPort(id1, 22, models.TCP)))
	require.NoError(t, db.Ports().Upsert(ctx, newTestPort(id2, 80, models.TCP)))

	require.NoError(t, db.Ports().DeleteByHost(ctx, id1))

	ports, err := db.Ports().ListByHost(ctx, id2)
	require.NoError(t, err)
	assert.Len(t, ports, 1, "deleting host1's ports should not affect host2's ports")
}
