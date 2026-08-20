// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package ecdf

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeJECDF(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755)
	require.NoError(t, err)
	return path
}

func setJECDFTool(t *testing.T, path string) {
	t.Helper()
	old := *jecdf
	*jecdf = path
	t.Cleanup(func() {
		*jecdf = old
	})
}

func jecdfExists(t *testing.T) bool {
	t.Helper()
	info, err := os.Stat(*jecdf)
	if err != nil {
		t.Log(err)
		return false
	}

	// Ensure it is a regular file and not a directory
	if info.IsDir() {
		t.Log("jecdf is a directory, not a file")
		return false
	}

	// Check if any execution bits (User, Group, or Other) are set
	if info.Mode().Perm()&0111 != 0 {
		return true
	}

	t.Log("file exists but is not executable")
	return false
}

func TestRunJECDF(t *testing.T) {
	setJECDFTool(t, writeFakeJECDF(t, `if [ "$1" != "query" ] || [ "$2" != "--mode=test" ]; then
	exit 2
fi
cat
`))

	input := []byte{0, 1, 2, 3, 255}
	var output bytes.Buffer
	err := runJECDF(
		context.Background(),
		[]string{"query", "--mode=test"},
		func(_ context.Context, stdin io.Writer) error {
			_, err := stdin.Write(input)
			return err
		},
		&output)

	require.NoError(t, err)
	assert.Equal(t, input, output.Bytes())
}

func TestJECDFCommandNameBoundsMetricLabels(t *testing.T) {
	assert.Equal(t, "build", jecdfCommandName([]string{"build", "-ulp", "3"}))
	assert.Equal(t, "build", jecdfCommandName([]string{"-someoption", "build", "-ulp", "3"}))
	assert.Equal(t, "query", jecdfCommandName([]string{"query", "1"}))
	assert.Equal(t, "render", jecdfCommandName([]string{"render", "128", "128", "2"}))
	assert.Equal(t, "unknown", jecdfCommandName(nil))
	assert.Equal(t, "unknown", jecdfCommandName([]string{"unexpected"}))
}
