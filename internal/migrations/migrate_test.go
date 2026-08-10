package migrations

import (
	"strings"
	"testing"
)

func TestBaselineIsTheOnlyMigration(t *testing.T) {
	items, err := load()
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(load()) = %d, want 1", len(items))
	}
	if items[0].Version != 1 || items[0].Name != "initial_schema" {
		t.Fatalf("migration = %#v, want version 1 initial_schema", items[0])
	}
}

func TestBaselineContainsNoPrivateDeploymentValues(t *testing.T) {
	items, err := load()
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	lower := strings.ToLower(items[0].SQL)
	for _, forbidden := range []string{"pc0", "http://pc", "'weewoo',\n    'http"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("baseline contains deployment-specific value %q", forbidden)
		}
	}
	if strings.Contains(lower, "alertmanager_host") || strings.Contains(lower, "9093") {
		t.Error("baseline contains an Alertmanager deployment default")
	}
}

func TestParseFilenameRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"not-valid.sql", "000000_bad.sql", "000001_.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) unexpectedly succeeded", name)
		}
	}
}
