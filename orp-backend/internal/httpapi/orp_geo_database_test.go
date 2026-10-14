package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeoIPManagedDatabaseTakesPrecedenceOverLegacyPath(t *testing.T) {
	root := t.TempDir()
	managedDir := filepath.Join(root, "managed")
	managedPath := filepath.Join(managedDir, geoIPDatabaseField)
	if err := os.MkdirAll(managedDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte("managed"), 0600); err != nil {
		t.Fatal(err)
	}
	externalPath := filepath.Join(root, "external.mmdb")
	if err := os.WriteFile(externalPath, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORP_GEOIP_STORAGE_DIR", managedDir)
	t.Setenv("ORP_GEOIP_DB_PATH", externalPath)

	got, source := activeGeoIPDatabasePath()
	if got != managedPath || source != "managed" {
		t.Fatalf("activeGeoIPDatabasePath() = (%q, %q), want (%q, managed)", got, source, managedPath)
	}
}

func TestActiveGeoIPDatabaseFallsBackToLegacyEnvironmentPath(t *testing.T) {
	t.Setenv("ORP_GEOIP_STORAGE_DIR", t.TempDir())
	externalPath := filepath.Join(t.TempDir(), "external.mmdb")
	if err := os.WriteFile(externalPath, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORP_GEOIP_DB_PATH", externalPath)

	got, source := activeGeoIPDatabasePath()
	if got != externalPath || source != "environment" {
		t.Fatalf("activeGeoIPDatabasePath() = (%q, %q), want (%q, environment)", got, source, externalPath)
	}
}

func TestInspectGeoIPCityDatabaseRejectsInvalidAndNonCityFiles(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "invalid.mmdb")
	if err := os.WriteFile(invalid, []byte("not a MaxMind database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectGeoIPCityDatabase(invalid); err == nil {
		t.Fatal("expected invalid database to be rejected")
	}
	for _, databaseType := range []string{"GeoLite2-City", "GeoIP2-City", "city-custom"} {
		if !isGeoIPCityDatabase(databaseType) {
			t.Errorf("expected %q to be recognized as a City database", databaseType)
		}
	}
	for _, databaseType := range []string{"GeoLite2-Country", "GeoIP2-ASN", ""} {
		if isGeoIPCityDatabase(databaseType) {
			t.Errorf("did not expect %q to be recognized as a City database", databaseType)
		}
	}
}
