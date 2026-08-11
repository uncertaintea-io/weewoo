package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type importJobStore interface {
	create(context.Context, *importJob) error
	update(context.Context, importJob) error
	list(context.Context) ([]importJob, error)
}

type databaseImportJobStore struct {
	db *sql.DB
}

func (s *databaseImportJobStore) create(ctx context.Context, job *importJob) error {
	return s.db.QueryRowContext(ctx, `
		INSERT INTO historical_import_job
			(service_id, state, progress, total_windows, imported_windows, gap_windows, error,
			 range_start, range_end, started_at, ended_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11)
		RETURNING id
	`, job.ServiceID, job.State, job.Progress, job.TotalWindows, job.ImportedWindows,
		job.GapWindows, job.Error, job.RangeStart, job.RangeEnd, job.StartedAt, job.EndedAt).Scan(&job.ID)
}

func (s *databaseImportJobStore) update(ctx context.Context, job importJob) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE historical_import_job
		SET state=$1, progress=$2, total_windows=$3, imported_windows=$4,
			gap_windows=$5, error=NULLIF($6,''), ended_at=$7
		WHERE id=$8
	`, job.State, job.Progress, job.TotalWindows, job.ImportedWindows,
		job.GapWindows, job.Error, job.EndedAt, job.ID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("historical import job %d not found", job.ID)
	}
	return nil
}

func (s *databaseImportJobStore) list(ctx context.Context) ([]importJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, service_id, state, progress, total_windows, imported_windows,
			gap_windows, COALESCE(error, ''), range_start, range_end, started_at, ended_at
		FROM historical_import_job
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]importJob, 0)
	for rows.Next() {
		var job importJob
		if err := rows.Scan(&job.ID, &job.ServiceID, &job.State, &job.Progress,
			&job.TotalWindows, &job.ImportedWindows, &job.GapWindows, &job.Error,
			&job.RangeStart, &job.RangeEnd, &job.StartedAt, &job.EndedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func interruptedImportEnd() *time.Time {
	now := time.Now().UTC()
	return &now
}
