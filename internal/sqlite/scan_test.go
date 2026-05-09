package sqlite_test

import (
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestScan(subnet string) *models.Scan {
	return &models.Scan{
		Subnet:     subnet,
		HostsFound: 5,
		StartedAt:  time.Now().UTC().Truncate(time.Second),
	}
}

func TestScanRepo_Create(t *testing.T) {
	scans := openTestDB(t).Scans()

	id, err := scans.Create(t.Context(), newTestScan("192.168.1.0/24"))
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestScanRepo_Create_NilFinishedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	s := newTestScan("10.0.0.0/8")
	s.FinishedAt = nil // scan is still in progress

	id, err := db.Scans().Create(ctx, s)
	require.NoError(t, err)

	list, err := db.Scans().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, id, list[0].ID)
	assert.Nil(t, list[0].FinishedAt, "FinishedAt should be nil for an in-progress scan")
}

func TestScanRepo_Create_WithFinishedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	finished := time.Now().UTC().Truncate(time.Second)
	s := newTestScan("10.0.0.0/8")
	s.FinishedAt = &finished

	_, err := db.Scans().Create(ctx, s)
	require.NoError(t, err)

	list, err := db.Scans().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NotNil(t, list[0].FinishedAt)
	assert.Equal(t, finished.Unix(), list[0].FinishedAt.Unix())
}

func TestScanRepo_List_Empty(t *testing.T) {
	scans := openTestDB(t).Scans()

	list, err := scans.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestScanRepo_List_OrderedNewestFirst(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	base := time.Now().UTC().Truncate(time.Second)
	for i, subnet := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		s := newTestScan(subnet)
		s.StartedAt = base.Add(time.Duration(i) * time.Minute)
		_, err := db.Scans().Create(ctx, s)
		require.NoError(t, err)
	}

	list, err := db.Scans().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "192.168.0.0/16", list[0].Subnet, "most recent scan should be first")
	assert.Equal(t, "10.0.0.0/8", list[2].Subnet, "oldest scan should be last")
}

func TestScanRepo_Finish(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	s := newTestScan("10.0.0.0/8")
	s.HostsFound = 0
	id, err := db.Scans().Create(ctx, s)
	require.NoError(t, err)

	finished := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.Scans().Finish(ctx, id, 99, finished))

	list, err := db.Scans().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 99, list[0].HostsFound)
	require.NotNil(t, list[0].FinishedAt)
	assert.Equal(t, finished.Unix(), list[0].FinishedAt.Unix())
}

func TestScanRepo_Create_PreservesFields(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	s := &models.Scan{
		Subnet:     "172.16.0.0/12",
		HostsFound: 42,
		StartedAt:  time.Now().UTC().Truncate(time.Second),
	}

	_, err := db.Scans().Create(ctx, s)
	require.NoError(t, err)

	list, err := db.Scans().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got := list[0]
	assert.Equal(t, s.Subnet, got.Subnet)
	assert.Equal(t, s.HostsFound, got.HostsFound)
	assert.Equal(t, s.StartedAt.Unix(), got.StartedAt.Unix())
}
