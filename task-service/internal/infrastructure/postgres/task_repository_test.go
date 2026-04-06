package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	gopg "github.com/go-pg/pg/v10"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/infrastructure/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupDB(t *testing.T) (*gopg.DB, func()) {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://test:test@%s:%s/testdb?sslmode=disable", host, port.Port())
	require.NoError(t, postgres.RunMigrations(dsn))

	db := gopg.Connect(&gopg.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port.Port()),
		User:     "test",
		Password: "test",
		Database: "testdb",
	})

	return db, func() {
		db.Close()
		container.Terminate(ctx)
	}
}

func fixedTask(id string) *task.Task {
	return task.NewTask(task.CreateParams{
		ID:          id,
		Cluster:     "test-cluster",
		Handler:     "test-handler",
		Namespace:   "kape-system",
		EventID:     "evt-" + id,
		EventSource: "alertmanager",
		EventType:   "kape.events.alertmanager",
		EventRaw:    task.EventRaw{"specversion": "1.0", "id": id},
		DryRun:      false,
		ReceivedAt:  time.Now().UTC().Add(-time.Minute),
	})
}

func TestTaskRepository_CreateAndFindByID(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	original := fixedTask("01CREATE")
	require.NoError(t, repo.Create(ctx, original))

	found, err := repo.FindByID(ctx, "01CREATE")
	require.NoError(t, err)
	assert.Equal(t, "01CREATE", found.ID)
	assert.Equal(t, task.StatusProcessing, found.Status)
	assert.Equal(t, "test-handler", found.Handler)
	assert.Equal(t, task.EventRaw{"specversion": "1.0", "id": "01CREATE"}, found.EventRaw)
}

func TestTaskRepository_FindByID_NotFound(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	_, err := repo.FindByID(context.Background(), "NONEXISTENT")
	assert.ErrorIs(t, err, task.ErrNotFound)
}

func TestTaskRepository_UpdateStatus(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, fixedTask("01UPDATE")))

	now := time.Now().UTC().Truncate(time.Millisecond)
	ms := 1234
	require.NoError(t, repo.UpdateStatus(ctx, "01UPDATE", task.StatusCompleted, task.UpdateFields{
		CompletedAt: &now,
		DurationMs:  &ms,
	}))

	found, err := repo.FindByID(ctx, "01UPDATE")
	require.NoError(t, err)
	assert.Equal(t, task.StatusCompleted, found.Status)
	assert.NotNil(t, found.CompletedAt)
	assert.Equal(t, ms, *found.DurationMs)
}

func TestTaskRepository_Delete(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, fixedTask("01DELETE")))
	require.NoError(t, repo.Delete(ctx, "01DELETE"))

	_, err := repo.FindByID(ctx, "01DELETE")
	assert.ErrorIs(t, err, task.ErrNotFound)
}

func TestTaskRepository_Delete_NotFound(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	err := repo.Delete(context.Background(), "GHOST")
	assert.ErrorIs(t, err, task.ErrNotFound)
}

func TestTaskRepository_List_FilterByHandler(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	a := fixedTask("01LSTA")
	a.Handler = "handler-a"
	b := fixedTask("01LSTB")
	b.Handler = "handler-a"
	c := fixedTask("01LSTC")
	c.Handler = "handler-b"

	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))
	require.NoError(t, repo.Create(ctx, c))

	tasks, total, err := repo.List(ctx, task.ListFilter{Handler: "handler-a", Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, tasks, 2)
	for _, tsk := range tasks {
		assert.Equal(t, "handler-a", tsk.Handler)
	}
}

func TestTaskRepository_List_FilterByStatus(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	p := fixedTask("01LSTP")
	c := fixedTask("01LSTC2")
	require.NoError(t, repo.Create(ctx, p))
	require.NoError(t, repo.Create(ctx, c))
	now := time.Now().UTC()
	require.NoError(t, repo.UpdateStatus(ctx, "01LSTC2", task.StatusCompleted, task.UpdateFields{CompletedAt: &now}))

	tasks, _, err := repo.List(ctx, task.ListFilter{Status: task.StatusCompleted, Limit: 50})
	require.NoError(t, err)
	for _, tsk := range tasks {
		assert.Equal(t, task.StatusCompleted, tsk.Status)
	}
}

func TestTaskRepository_List_Cursor(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	// Create 3 tasks
	for _, id := range []string{"01PAGE1", "01PAGE2", "01PAGE3"} {
		require.NoError(t, repo.Create(ctx, fixedTask(id)))
	}

	// Get first page
	page1, _, err := repo.List(ctx, task.ListFilter{Limit: 2, Sort: "received_at:asc"})
	require.NoError(t, err)
	require.Len(t, page1, 2)

	// Get second page using cursor from last item on page 1
	page2, _, err := repo.List(ctx, task.ListFilter{Limit: 2, Sort: "received_at:asc", Cursor: page1[1].ID})
	require.NoError(t, err)
	require.NotEmpty(t, page2)
	// Second page must not overlap with first
	for _, t2 := range page2 {
		for _, t1 := range page1 {
			assert.NotEqual(t, t1.ID, t2.ID)
		}
	}
}

func TestTaskRepository_Lineage(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	// original task
	root := fixedTask("01ROOT")
	require.NoError(t, repo.Create(ctx, root))

	// retry task
	retry1 := fixedTask("01RETRY1")
	retry1.RetryOf = strPtr("01ROOT")
	require.NoError(t, repo.Create(ctx, retry1))

	chain, err := repo.Lineage(ctx, "01RETRY1")
	require.NoError(t, err)
	require.Len(t, chain, 2)
	assert.Equal(t, "01ROOT", chain[0].ID)
	assert.Equal(t, "01RETRY1", chain[1].ID)
}

func TestTaskRepository_BulkTimeout(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	// old processing task
	old := fixedTask("01OLD")
	old.ReceivedAt = time.Now().UTC().Add(-2 * time.Hour)
	require.NoError(t, repo.Create(ctx, old))

	// recent processing task — should NOT be timed out
	recent := fixedTask("01RECENT")
	recent.ReceivedAt = time.Now().UTC()
	require.NoError(t, repo.Create(ctx, recent))

	affected, err := repo.BulkTimeout(ctx, 3600) // 1 hour threshold
	require.NoError(t, err)
	assert.Equal(t, []string{"01OLD"}, affected)

	found, err := repo.FindByID(ctx, "01OLD")
	require.NoError(t, err)
	assert.Equal(t, task.StatusTimeout, found.Status)

	found2, err := repo.FindByID(ctx, "01RECENT")
	require.NoError(t, err)
	assert.Equal(t, task.StatusProcessing, found2.Status)
}

func TestTaskRepository_EnsurePartition_Idempotent(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	month := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, repo.EnsurePartition(ctx, month))
	// Idempotent — second call must not error
	require.NoError(t, repo.EnsurePartition(ctx, month))
}

func strPtr(s string) *string { return &s }
