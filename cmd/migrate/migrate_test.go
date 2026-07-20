package main

import (
	"strings"
	"testing"
)

func TestLoadMigrations(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	for i, migration := range migrations {
		wantVersion := int64(i + 1)
		if migration.Version != wantVersion {
			t.Fatalf("migration[%d].Version = %d, want %d", i, migration.Version, wantVersion)
		}
		if migration.Name == "" {
			t.Fatalf("migration[%d].Name is empty", i)
		}
		if migration.SQL == "" {
			t.Fatalf("migration[%d].SQL is empty", i)
		}
	}
}

func TestECDFMigrationCanResumeWhenIndexesAlreadyExist(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, migration := range migrations {
		if migration.Version == 9 {
			sql = migration.SQL
		}
	}
	if !strings.Contains(sql, "CREATE UNIQUE INDEX IF NOT EXISTS ecdf_build_interval_idx") {
		t.Fatal("migration 9 must tolerate an existing ECDF build interval index")
	}
}

func TestParseMigrationFilenameRejectsInvalidName(t *testing.T) {
	if _, _, err := parseMigrationFilename("not-valid.sql"); err == nil {
		t.Fatal("parseMigrationFilename() unexpectedly succeeded")
	}
}
