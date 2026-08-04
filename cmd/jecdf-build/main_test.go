package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestBuildActiveGenerationUsesOnlyCurrentGeneration(t *testing.T) {
	fakeTool := filepath.Join(t.TempDir(), "jecdf")
	require.NoError(t, os.WriteFile(fakeTool, []byte("#!/bin/sh\ncat\n"), 0755))
	oldTool := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", fakeTool))
	t.Cleanup(func() { require.NoError(t, flag.Set("jecdf", oldTool)) })

	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api", Generation: 2}))
	chunks := ecdf.NewFakeChunkStore()
	oldChunk, err := ecdf.Encode(time.Unix(1, 0), []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	newChunk, err := ecdf.Encode(time.Unix(2, 0), []ecdf.Sample{{Value: 3, Count: 1}}, []ecdf.Sample{{Value: 4, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, 1, 1, time.Unix(1, 0), oldChunk))
	require.NoError(t, chunks.WriteChunk(7, 1, 2, time.Unix(2, 0), newChunk))

	var out bytes.Buffer
	eligible, generation, err := buildActiveGeneration(context.Background(), cfg, chunks, 7, 1, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, eligible)
	assert.Equal(t, int64(2), generation)
	assert.Equal(t, newChunk, out.Bytes())
}

func TestBuildActiveGenerationReportsDiagnosticContext(t *testing.T) {
	fakeTool := filepath.Join(t.TempDir(), "jecdf")
	require.NoError(t, os.WriteFile(fakeTool, []byte("#!/bin/sh\nexit 1\n"), 0755))
	oldTool := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", fakeTool))
	t.Cleanup(func() { require.NoError(t, flag.Set("jecdf", oldTool)) })

	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api", Generation: 3}))
	chunks := ecdf.NewFakeChunkStore()
	chunk, err := ecdf.Encode(time.Unix(1, 0), []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, 1, 3, time.Unix(1, 0), chunk))

	_, _, err = buildActiveGeneration(context.Background(), cfg, chunks, 7, 1, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service 7 indicator 1 generation 3 from 1 eligible chunks")
	assert.Contains(t, err.Error(), "jecdf failed: exit status 1")
}
