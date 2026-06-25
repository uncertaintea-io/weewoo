package collection

import (
	"net/http"
	"testing"
	"time"
)

// this tests the schedule function
func TestSchedule(t *testing.T) {
	collector := NewCollector(http.DefaultClient, nil, WeeWooService)
	collector.Schedule(WeeWooService)
	collector.Start()
	collector.Stop()

	// this tests the next collection time function to make sure the math is correct
	for i := 0; i < 10; i++ {
		now := time.Now().Truncate(time.Minute).Add(time.Duration(i)*time.Minute)
		next, err := NextCollectionTime(WeeWooService, now)
		if err != nil {
			t.Fatalf("next collection time: %v", err)
		}
		if next != now.Add(time.Minute) {
			t.Fatalf("next collection time: %v, expected: %v", next, now.Add(time.Minute))
		}
	}
}
