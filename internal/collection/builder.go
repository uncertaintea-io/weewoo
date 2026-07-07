package collection

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	ecdfBuilderCallbackID = 1
	ecdfBuilderServiceID  = 1
	ecdfBuilderInterval   = time.Hour
)

// StartECDFBuilder schedules one hourly builder callback. The scheduler owns the
// timer loop and runs callbacks in goroutines when their window is due.
func StartECDFBuilder(chunkStore ecdf.ChunkStore, database *sql.DB, scheduler *IntervalScheduler) error {
	if scheduler == nil {
		return fmt.Errorf("nil interval scheduler")
	}
	return scheduler.AddCallback(ecdfBuilderCallbackID, ecdfBuilderInterval, func(_ context.Context, start time.Time, end time.Time) IntervalResult {
		out, err := BuildECDF(chunkStore, database, ecdfBuilderServiceID, LoadLatencyIndicator, start, end)
		if err != nil {
			return IntervalRetry(err)
		}
		slog.Info("built joint ECDF", "service_id", ecdfBuilderServiceID, "indicator_id", LoadLatencyIndicator, "start", start, "end", end, "bytes", len(out))
		return IntervalSuccess()
	}, WithLastEnd(time.Now().Add(-ecdfBuilderInterval)))
}

func BuildECDF(chunkStore ecdf.ChunkStore, _ *sql.DB, serviceID int, indicatorID int, start time.Time, end time.Time) ([]byte, error) {
	return ecdf.BuildJointECDF(chunkStore, serviceID, indicatorID, start, end)
}
