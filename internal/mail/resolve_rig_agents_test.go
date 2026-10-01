package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// Rig-scoped agent beads (polecats included) live in the owning rig DB
// (gs-8hj), so pattern expansion must list rig databases too: a polecat
// whose bead exists only in its rig DB still matches "gastown/polecats/*".
func TestResolvePattern_IncludesRigLocalAgentBeads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix shell script mock for bd")
	}
	townRoot, _ := filepath.EvalSymlinks(t.TempDir())
	townBeads := filepath.Join(townRoot, ".beads")
	rigBeads := filepath.Join(townRoot, "gastown", "mayor", "rig", ".beads")
	for _, dir := range []string{filepath.Join(townRoot, "mayor"), townBeads, rigBeads} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := beads.WriteRoutes(townBeads, []beads.Route{{Prefix: "hq-", Path: "."}, {Prefix: "gt-", Path: "gastown/mayor/rig"}}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    list)
      if [ "${BEADS_DIR:-}" = %q ]; then
        echo '[{"id":"gt-gastown-polecat-rust","title":"rust","issue_type":"task","labels":["gt:agent"],"status":"open"}]'
      else
        echo '[{"id":"hq-mayor","title":"mayor","issue_type":"task","labels":["gt:agent"],"status":"open"},{"id":"gt-gastown-polecat-rust","title":"stale town duplicate","issue_type":"task","labels":["gt:agent"],"status":"open"}]'
      fi
      exit 0
      ;;
    mol) echo '[]'; exit 0 ;;
  esac
done
exit 0
`, rigBeads)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := NewResolver(beads.New(townRoot), townRoot)
	recipients, err := r.resolvePattern("gastown/polecats/*")
	if err != nil {
		t.Fatalf("resolvePattern: %v", err)
	}
	if len(recipients) != 1 || recipients[0].Address != "gastown/polecats/rust" {
		t.Fatalf("resolvePattern = %+v, want the rig-local polecat gastown/polecats/rust", recipients)
	}

	agents, err := beads.New(townRoot).ListAgentBeadsWithRigs()
	if err != nil {
		t.Fatalf("ListAgentBeadsWithRigs: %v", err)
	}
	if agents["hq-mayor"] == nil || agents["gt-gastown-polecat-rust"] == nil || agents["gt-gastown-polecat-rust"].Title != "rust" {
		t.Fatalf("ListAgentBeadsWithRigs = %v, want town roles plus the rig copy winning over the town duplicate", agents)
	}
}
