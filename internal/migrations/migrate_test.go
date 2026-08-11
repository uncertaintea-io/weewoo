package migrations

import (
	"testing"
)

func TestParseFilenameRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"not-valid.sql", "000000_bad.sql", "000001_.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) unexpectedly succeeded", name)
		}
	}
}
