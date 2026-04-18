package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	gopg "github.com/go-pg/pg/v10"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/application/query"
	"github.com/kape-io/kape/task-service/internal/infrastructure/postgres"
	"github.com/kape-io/kape/task-service/internal/infrastructure/sse"
	httpAdapter "github.com/kape-io/kape/task-service/internal/interfaces/http"
)

func main() {
	// Config from environment
	pgDSN := envOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/kape?sslmode=disable")
	addr := envOrDefault("ADDR", ":8080")

	// Run migrations
	if err := postgres.RunMigrations(pgDSN); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	// Connect DB
	opts, err := gopg.ParseURL(pgDSN)
	if err != nil {
		log.Fatalf("parse DSN: %v", err)
	}
	db := gopg.Connect(opts)
	defer db.Close()

	// Ensure current and next month partitions exist
	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, month := range []time.Time{now, now.AddDate(0, 1, 0)} {
		if err := repo.EnsurePartition(ctx, month); err != nil {
			log.Fatalf("ensure partition: %v", err)
		}
	}

	// Infrastructure
	hub := sse.NewHub()

	// Application
	createTask := command.NewCreateTaskCommand(repo, hub)
	updateStatus := command.NewUpdateStatusCommand(repo, hub)
	deleteTask := command.NewDeleteTaskCommand(repo)
	bulkTimeout := command.NewBulkUpdateStatusCommand(repo, hub)
	getTask := query.NewGetTaskQuery(repo)
	listTasks := query.NewListTasksQuery(repo)
	taskLineage := query.NewTaskLineageQuery(repo)

	// HTTP adapter
	srv := httpAdapter.NewServer(
		createTask, updateStatus, deleteTask, bulkTimeout,
		getTask, listTasks, taskLineage,
	)
	sseHandler := httpAdapter.NewSSEHandler(hub)

	// Router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	srv.Routes(r, sseHandler)

	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
