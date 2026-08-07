package collection

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestKSCountAndScaledStatisticByBucketPopulation(t *testing.T) {
	tests := []struct {
		name           string
		countPerBucket uint64
	}{
		{name: "one observation per bucket", countPerBucket: 1},
		{name: "one hundred observations per bucket", countPerBucket: 100},
		{name: "one thousand observations per bucket", countPerBucket: 1_000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const bucketCount = 10
			latencies := make([]ecdf.Sample, 0, bucketCount)
			for bucket := 1; bucket <= bucketCount; bucket++ {
				latencies = append(latencies, ecdf.Sample{
					Value: float64(bucket) / bucketCount,
					Count: test.countPerBucket,
				})
			}

			observationCount, err := checkedSampleCount(latencies)
			require.NoError(t, err)
			result := oneSampleKS(
				func(value float64) float64 { return value },
				latencies,
				observationCount,
			)
			bucketCountUsedForKS := uint64(len(latencies))
			z := result.Statistic * math.Sqrt(float64(bucketCountUsedForKS))

			t.Logf(
				"buckets(n)=%d count_per_bucket=%d observation_count=%d D=%.17g z=%.17g p_value=%.17g",
				bucketCountUsedForKS, test.countPerBucket, observationCount,
				result.Statistic, z, result.PValue,
			)

			require.Equal(t, uint64(bucketCount)*test.countPerBucket, observationCount)
			require.InDelta(t, 0.1, result.Statistic, 1e-15)
			require.InDelta(t, 0.99996523065407195, result.PValue, 1e-15)
		})
	}
}
