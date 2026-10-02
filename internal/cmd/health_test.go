package cmd

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// stubHealthDatabases replaces the live SHOW DATABASES lookup for one test.
func stubHealthDatabases(t *testing.T, names []string, err error) {
	t.Helper()
	orig := listHealthDatabases
	listHealthDatabases = func(int) ([]string, error) { return names, err }
	t.Cleanup(func() { listHealthDatabases = orig })
}

func TestDiscoverHealthDatabases_UsesServerListing(t *testing.T) {
	stubHealthDatabases(t, []string{"hq", "gastown_src", "booktoon", "usage_monitor"}, nil)

	got, err := discoverHealthDatabases(3307)
	if err != nil {
		t.Fatalf("discoverHealthDatabases: %v", err)
	}
	want := []string{"booktoon", "gastown_src", "hq", "usage_monitor"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, stale := range []string{"gt", "mo"} {
		for _, name := range got {
			if name == stale {
				t.Errorf("stale hardcoded database %q reported though server does not serve it", stale)
			}
		}
	}
}

func TestDiscoverHealthDatabases_PropagatesListError(t *testing.T) {
	stubHealthDatabases(t, nil, errors.New("connection refused"))

	got, err := discoverHealthDatabases(3307)
	if err == nil {
		t.Fatal("expected error when server listing fails")
	}
	if got != nil {
		t.Errorf("expected no databases on error, got %v", got)
	}
}

func TestSelectHealthDatabases_ExcludesSystemDatabases(t *testing.T) {
	got := selectHealthDatabases([]string{
		"information_schema", "hq", "mysql", "INFORMATION_SCHEMA", "dolt_cluster", "gastown_src",
	})
	want := []string{"gastown_src", "hq"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSelectHealthDatabases_DeterministicOrder(t *testing.T) {
	a := selectHealthDatabases([]string{"usage_monitor", "hq", "booktoon", "gastown_src"})
	b := selectHealthDatabases([]string{"gastown_src", "booktoon", "usage_monitor", "hq"})
	want := []string{"booktoon", "gastown_src", "hq", "usage_monitor"}
	if !reflect.DeepEqual(a, want) || !reflect.DeepEqual(b, want) {
		t.Errorf("ordering not deterministic: %v vs %v, want %v", a, b, want)
	}
}

func TestSelectHealthDatabases_DropsDuplicatesAndEmpty(t *testing.T) {
	got := selectHealthDatabases([]string{"hq", "", "hq", "gastown_src"})
	want := []string{"gastown_src", "hq"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSelectHealthDatabases_OnlySystemDatabases(t *testing.T) {
	got := selectHealthDatabases([]string{"information_schema", "mysql"})
	if len(got) != 0 {
		t.Errorf("expected no user databases, got %v", got)
	}
}

func TestPrintHealthReport_DistinguishesUnavailableDatabase(t *testing.T) {
	report := &HealthReport{
		Server: &ServerHealth{Running: true},
		Databases: []DatabaseHealth{
			{Name: "empty_rig"},
			{Name: "hq", Issues: 5, OpenIssues: 2, Commits: 9},
			{Name: "not_beads", Error: "table not found: issues"},
		},
		Backups:   &BackupHealth{},
		Processes: &ProcessHealth{},
	}

	out := captureStdout(t, func() { printHealthReport(report) })

	if !strings.Contains(out, "empty_rig") || !strings.Contains(out, "0 issues") {
		t.Errorf("empty rig DB should still be listed with zero counts:\n%s", out)
	}
	if !strings.Contains(out, "5 issues (2 open)") {
		t.Errorf("rig DB counts missing:\n%s", out)
	}
	if !strings.Contains(out, "unavailable") || !strings.Contains(out, "table not found: issues") {
		t.Errorf("unavailable DB not distinguished:\n%s", out)
	}
	if strings.Count(out, "0 issues") != 1 {
		t.Errorf("unavailable DB rendered as phantom zero counts:\n%s", out)
	}
}

func TestPrintHealthReport_ShowsEnumerationError(t *testing.T) {
	report := &HealthReport{
		Server:         &ServerHealth{Running: true},
		DatabasesError: "SHOW DATABASES: connection refused",
		Backups:        &BackupHealth{},
		Processes:      &ProcessHealth{},
	}

	out := captureStdout(t, func() { printHealthReport(report) })

	if !strings.Contains(out, "Could not list databases") || !strings.Contains(out, "connection refused") {
		t.Errorf("enumeration failure not reported:\n%s", out)
	}
}

func TestHealthReportJSON_ShapePreserved(t *testing.T) {
	report := HealthReport{
		Databases: []DatabaseHealth{{Name: "hq", Issues: 1}},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["databases_error"]; ok {
		t.Error("databases_error should be omitted when enumeration succeeded")
	}
	var dbs []map[string]json.RawMessage
	if err := json.Unmarshal(decoded["databases"], &dbs); err != nil {
		t.Fatalf("unmarshal databases: %v", err)
	}
	for _, key := range []string{"name", "issues", "open_issues", "wisps", "open_wisps", "commits"} {
		if _, ok := dbs[0][key]; !ok {
			t.Errorf("database entry missing key %q", key)
		}
	}
	if _, ok := dbs[0]["error"]; ok {
		t.Error("error should be omitted for a healthy database")
	}
}
