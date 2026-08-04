// Command jecdf-build builds a joint ECDF directly from the active service
// generation. It is intended as a diagnostic tool and does not publish to the
// database.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type serviceReader interface {
	ReadService(int) (*config.Service, error)
}

func buildActiveGeneration(ctx context.Context, services serviceReader, chunks ecdf.ChunkStore, serviceID, indicatorID int, out io.Writer) (int, int64, error) {
	service, err := services.ReadService(serviceID)
	if err != nil {
		return 0, 0, fmt.Errorf("read service %d: %w", serviceID, err)
	}
	eligible, err := chunks.CountEligibleChunks(ctx, serviceID, indicatorID, service.Generation)
	if err != nil {
		return 0, service.Generation, fmt.Errorf("count eligible chunks: %w", err)
	}
	if err := ecdf.BuildJointECDFContextGeneration(ctx, chunks, serviceID, indicatorID, service.Generation, out); err != nil {
		return eligible, service.Generation, fmt.Errorf("build service %d indicator %d generation %d from %d eligible chunks: %w",
			serviceID, indicatorID, service.Generation, eligible, err)
	}
	return eligible, service.Generation, nil
}

func main() {
	configFile := flag.String("config", "config.yaml", "config file")
	serviceID := flag.Int("service-id", 0, "service ID to build (required)")
	indicatorID := flag.Int("indicator-id", 1, "indicator ID to build")
	output := flag.String("output", "jecdf-debug.bin", "output file for the generated JECDF")
	timeout := flag.Duration("timeout", 5*time.Minute, "maximum build duration")
	flag.Parse()

	if *serviceID <= 0 || *indicatorID <= 0 || *output == "" || *timeout <= 0 {
		flag.Usage()
		os.Exit(2)
	}

	settings, err := config.ReadSystemSettings(*configFile)
	if err != nil {
		log.Fatalf("read system settings: %v", err)
	}
	db, err := settings.OpenDatabase()
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	destination, err := filepath.Abs(*output)
	if err != nil {
		log.Fatalf("resolve output path: %v", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".jecdf-build-*")
	if err != nil {
		log.Fatalf("create temporary output: %v", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cfg := config.NewDatabaseConfig(db)
	eligible, generation, buildErr := buildActiveGeneration(ctx, cfg, ecdf.NewDatabaseChunkStore(db), *serviceID, *indicatorID, temporary)
	closeErr := temporary.Close()
	if buildErr != nil {
		log.Fatalf("JECDF generation failed: %v", buildErr)
	}
	if closeErr != nil {
		log.Fatalf("close generated JECDF: %v", closeErr)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		log.Fatalf("install generated JECDF: %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		log.Fatalf("inspect generated JECDF: %v", err)
	}
	log.Printf("built service %d indicator %d generation %d from %d eligible chunks: %s (%d bytes)",
		*serviceID, *indicatorID, generation, eligible, destination, info.Size())
}
