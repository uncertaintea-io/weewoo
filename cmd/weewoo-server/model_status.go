package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type databaseModelStatusReader struct {
	db     *sql.DB
	cfg    config.Config
	chunks ecdf.ChunkStore
}

func (r *databaseModelStatusReader) ModelStatus(ctx context.Context, service *config.Service, indicatorID int) (modelStatus, error) {
	readiness, err := collection.ReadModelReadiness(ctx, r.cfg, r.chunks, service, indicatorID)
	if err != nil {
		return modelStatus{}, fmt.Errorf("read model readiness: %w", err)
	}
	status := modelStatus{State: "learning", Coverage: readiness.Coverage, Required: readiness.Required}
	if readiness.Ready {
		status.State = "ready"
	}
	var latest sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT max(created_at) FROM ecdf WHERE service_id=$1 AND indicator_id=$2`, service.Id, indicatorID).Scan(&latest); err != nil {
		return status, err
	}
	if latest.Valid {
		status.LatestBuild = &latest.Time
	}
	return status, nil
}
