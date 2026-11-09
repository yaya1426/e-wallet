//go:build integration

package database_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/database/databasetest"
	"github.com/yaya1426/e-wallet/migrations"
)

func newMigrator(t *testing.T) (*database.Migrator, string) {
	t.Helper()
	url := databasetest.Shared(t).NewDatabase(t)
	db, err := database.Open(context.Background(), url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	t.Cleanup(db.Close)
	migrator, err := database.NewMigrator(db.Pool(), url)
	require.NoError(t, err)
	return migrator, url
}

func TestOpen_PingsDatabase(t *testing.T) {
	url := databasetest.Shared(t).NewDatabase(t)

	db, err := database.Open(context.Background(), url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	defer db.Close()

	assert.NoError(t, db.Ping(context.Background()))
}

func TestOpen_InvalidURL(t *testing.T) {
	_, err := database.Open(context.Background(), "not a url ::", database.Options{Tx: database.DefaultTxSettings()})
	assert.Error(t, err)
}

func TestMigrate_UpIdempotent_StatusApplied(t *testing.T) {
	ctx := context.Background()
	migrator, _ := newMigrator(t)

	before, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, database.MigrationStatus{Version: 0, Dirty: false, Latest: latestMigration(t)}, before)
	assert.True(t, before.Pending())

	require.NoError(t, migrator.Up(ctx))
	require.NoError(t, migrator.Up(ctx), "second run is a no-op")

	after, err := migrator.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, database.MigrationStatus{Version: latestMigration(t), Dirty: false, Latest: latestMigration(t)}, after)
	assert.False(t, after.Pending())
}

func TestMigrate_DirtyVersion_Fails(t *testing.T) {
	ctx := context.Background()
	migrator, url := newMigrator(t)
	require.NoError(t, migrator.Up(ctx))

	db, err := database.Open(ctx, url, database.Options{Tx: database.DefaultTxSettings()})
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Pool().Exec(ctx, `UPDATE schema_migrations SET dirty = true`)
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

// latestMigration counts the embedded up migrations; versions are sequential from 1 (TDR-014).
func latestMigration(t *testing.T) uint {
	t.Helper()
	ups, err := fs.Glob(migrations.FS, "*.up.sql")
	require.NoError(t, err)
	return uint(len(ups))
}
