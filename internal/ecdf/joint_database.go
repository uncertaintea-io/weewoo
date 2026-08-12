package ecdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	databaseutil "github.com/uncertaintea-io/weewoo/internal/database"
)

const retainedJointECDFVersions = 5

type databaseJointStore struct {
	db     *sql.DB
	sqlite bool
}

func NewDatabaseJointStore(db *sql.DB) JointStore {
	return &databaseJointStore{db: db, sqlite: databaseutil.IsSQLite(db)}
}

func (s *databaseJointStore) Publish(ctx context.Context, serviceID, indicatorID int, intervalEnd time.Time, build func(io.Writer) error) (bytesWritten int64, published bool, err error) {
	if build == nil {
		return 0, false, errors.New("nil ECDF build function")
	}
	if intervalEnd.IsZero() {
		return 0, false, errors.New("zero ECDF build interval end")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("acquire ECDF database connection: %w", err)
	}
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	if !s.sqlite {
		var acquired bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, serviceID, indicatorID).Scan(&acquired); err != nil {
			return 0, false, fmt.Errorf("acquire ECDF publication lock: %w", err)
		}
		if !acquired {
			slog.Info(
				"ECDF publication skipped because PostgreSQL advisory lock is held",
				"service_id", serviceID,
				"indicator_id", indicatorID,
				"coordination_mode", "postgres_advisory_lock",
			)
			return 0, false, nil
		}
		defer func() {
			var unlocked bool
			unlockErr := conn.QueryRowContext(context.Background(), `SELECT pg_advisory_unlock($1, $2)`, serviceID, indicatorID).Scan(&unlocked)
			if unlockErr != nil || !unlocked {
				if unlockErr == nil {
					unlockErr = errors.New("database reported lock was not held")
				}
				err = errors.Join(err, fmt.Errorf("release ECDF publication lock: %w", unlockErr))
			}
		}()
	}

	const alreadyPublishedQuery = `
		SELECT EXISTS (
			SELECT 1 FROM ecdf
			WHERE service_id = $1 AND indicator_id = $2 AND interval_end = $3
		)
	`
	var alreadyPublished bool
	if err := conn.QueryRowContext(ctx, alreadyPublishedQuery, serviceID, indicatorID, intervalEnd).Scan(&alreadyPublished); err != nil {
		return 0, false, fmt.Errorf("check ECDF build interval: %w", err)
	}
	if alreadyPublished {
		return 0, false, nil
	}
	if s.sqlite {
		if err := conn.Close(); err != nil {
			return 0, false, fmt.Errorf("release ECDF database connection for build: %w", err)
		}
		conn = nil
	}

	var body bytes.Buffer
	if err := build(&body); err != nil {
		return 0, false, err
	}
	if body.Len() == 0 {
		return 0, false, errors.New("built ECDF is empty")
	}
	if s.sqlite {
		conn, err = s.db.Conn(ctx)
		if err != nil {
			return 0, false, fmt.Errorf("reacquire ECDF database connection after build: %w", err)
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin ECDF publication: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if s.sqlite {
		if err := tx.QueryRowContext(ctx, alreadyPublishedQuery, serviceID, indicatorID, intervalEnd).Scan(&alreadyPublished); err != nil {
			return 0, false, fmt.Errorf("recheck ECDF build interval: %w", err)
		}
		if alreadyPublished {
			return 0, false, nil
		}
	}

	var version int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM ecdf
		WHERE service_id = $1 AND indicator_id = $2
	`, serviceID, indicatorID).Scan(&version); err != nil {
		return 0, false, fmt.Errorf("choose ECDF version: %w", err)
	}
	sum := sha256.Sum256(body.Bytes())
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ecdf (service_id, indicator_id, version, interval_end, body, bytes, sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, serviceID, indicatorID, version, intervalEnd, body.Bytes(), body.Len(), hex.EncodeToString(sum[:])); err != nil {
		return 0, false, fmt.Errorf("insert ECDF version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM ecdf
		WHERE service_id = $1
		  AND indicator_id = $2
		  AND version NOT IN (
			SELECT version FROM ecdf
			WHERE service_id = $1 AND indicator_id = $2
			ORDER BY version DESC
			LIMIT $3
		  )
	`, serviceID, indicatorID, retainedJointECDFVersions); err != nil {
		return 0, false, fmt.Errorf("remove old ECDF versions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit ECDF publication: %w", err)
	}
	return int64(body.Len()), true, nil
}

func (s *databaseJointStore) ReadCurrent(ctx context.Context, serviceID, indicatorID int) ([]byte, string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT body, bytes, sha256
		FROM ecdf
		WHERE service_id = $1 AND indicator_id = $2
		ORDER BY version DESC
		LIMIT $3
	`, serviceID, indicatorID, retainedJointECDFVersions)
	if err != nil {
		return nil, "", fmt.Errorf("read current ECDF: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var body []byte
		var size int64
		var expectedHash string
		if err := rows.Scan(&body, &size, &expectedHash); err != nil {
			return nil, "", fmt.Errorf("scan ECDF version: %w", err)
		}
		if int64(len(body)) != size {
			slog.Warn(
				"skipping ECDF version because body length does not match stored size",
				"service_id", serviceID,
				"indicator_id", indicatorID,
				"expected_size", size,
				"actual_size", len(body),
			)
			continue
		}
		sum := sha256.Sum256(body)
		if actualHash := hex.EncodeToString(sum[:]); actualHash != expectedHash {
			slog.Warn(
				"skipping ECDF version because body does not match stored checksum",
				"service_id", serviceID,
				"indicator_id", indicatorID,
				"expected_hash", expectedHash,
				"actual_hash", actualHash,
			)
			continue
		}
		return body, expectedHash, nil
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate ECDF versions: %w", err)
	}
	return nil, "", fmt.Errorf("read current ECDF: %w", sql.ErrNoRows)
}
