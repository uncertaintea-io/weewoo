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

	ctx := context.Background()
	input := []byte{0, 1, 2, 3, 255}
	var output bytes.Buffer
	err := runJECDF(
		ctx,
		[]string{"query", "--mode=test"},
		func(_ context.Context, stdin io.Writer) error {
			_, err := stdin.Write(input)
			return err
		},
		&output)

	require.NoError(t, err)
	assert.Equal(t, input, output.Bytes())
}
