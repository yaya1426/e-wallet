//go:build integration

package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
)

func newMigrator(t *testing.T) (*database.Migrator, string) {
	t.Helper()
	url := databasetest.Shared(t).NewDatabase(t)
	pool, err := database.Open(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	migrator, err := database.NewMigrator(pool, url)
	require.NoError(t, err)
	return migrator, url
}

func TestOpen_PingsDatabase(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)

	pool, err := database.Open(context.Background(), url)
	require.NoError(t, err)
	defer pool.Close()

	assert.NoError(t, pool.Ping(context.Background()))
}

func TestOpen_InvalidURL(t *testing.T) {
	_, err := database.Open(context.Background(), "not a url ::")
	assert.Error(t, err)
}

func TestMigrate_UpIdempotent_StatusApplied(t *testing.T) {
	ctx := context.Background()
	migrator, _ := newMigrator(t)

	before, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, database.MigrationStatus{Version: 0, Dirty: false, Latest: 1}, before)
	assert.True(t, before.Pending())

	require.NoError(t, migrator.Up(ctx))
	require.NoError(t, migrator.Up(ctx), "second run is a no-op")

	after, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, database.MigrationStatus{Version: 1, Dirty: false, Latest: 1}, after)
	assert.False(t, after.Pending())
}

func TestMigrate_DirtyVersion_Fails(t *testing.T) {
	ctx := context.Background()
	migrator, url := newMigrator(t)
	require.NoError(t, migrator.Up(ctx))

	pool, err := database.Open(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `UPDATE schema_migrations SET dirty = true`)
	require.NoError(t, err)

	status, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.True(t, status.Dirty)

	err = migrator.Up(ctx)
	require.ErrorIs(t, err, database.ErrDirty)
	assert.Contains(t, err.Error(), "force")
}

func TestMigrate_CancelledContext_ReportsInterrupted(t *testing.T) {
	migrator, _ := newMigrator(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := migrator.Up(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "interrupted")
}
