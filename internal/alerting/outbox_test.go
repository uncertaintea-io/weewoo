package alerting

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestSQLiteFailedDeliveryStoresComparableRetryTimestamp(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/weewoo.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
		CREATE TABLE alert (
			id INTEGER PRIMARY KEY,
			alertmanager_state TEXT,
			alertmanager_error TEXT,
			updated_at TIMESTAMP
		);
		CREATE TABLE alert_outbox (
			id INTEGER PRIMARY KEY,
			alert_id INTEGER NOT NULL,
			next_attempt_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_error TEXT
		);
		INSERT INTO alert (id) VALUES (1);
		INSERT INTO alert_outbox (id, alert_id) VALUES (1, 1)
	`)
	require.NoError(t, err)

	deliveryErr := errors.New("Alertmanager unavailable")
	dispatcher := &OutboxDispatcher{db: db, ctx: context.Background(), sqlite: true}
	assert.ErrorIs(t, dispatcher.markFailed(1, 1, 1, deliveryErr), deliveryErr)

	var eligible bool
	var stored string
	require.NoError(t, db.QueryRow(`SELECT CAST(next_attempt_at AS TEXT) FROM alert_outbox WHERE id=1`).Scan(&stored))
	_, err = time.ParseInLocation(time.DateTime, stored, time.UTC)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`
		SELECT next_attempt_at <= datetime(CURRENT_TIMESTAMP, '+5 seconds')
		FROM alert_outbox WHERE id=1
	`).Scan(&eligible))
	assert.True(t, eligible)
}
