package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/tmux"
)

// polecatLocalityTown builds a town whose rig "myr" (prefix gt-) keeps its
// beads at myr/mayor/rig/.beads, plus a bd mock that logs BEADS_DIR per call.
// Polecat agent beads exist only in the rig database (gs-8hj).
func polecatLocalityTown(t *testing.T) (townRoot, townBeads, rigBeads, logPath, bdPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix shell script mocks for bd")
	}
	townRoot, _ = filepath.EvalSymlinks(t.TempDir())
	townBeads = filepath.Join(townRoot, ".beads")
	rigBeads = filepath.Join(townRoot, "myr", "mayor", "rig", ".beads")
	for _, dir := range []string{filepath.Join(townRoot, "mayor"), townBeads, rigBeads} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := beads.WriteRoutes(townBeads, []beads.Route{{Prefix: "hq-", Path: "."}, {Prefix: "gt-", Path: "myr/mayor/rig"}}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logPath = filepath.Join(binDir, "bd.log")
	agent := `[{"id":"gt-myr-polecat-mycat","issue_type":"agent","labels":["gt:agent"],"description":"agent_state: working","hook_bead":"gt-work","agent_state":"working","updated_at":"2026-01-01T00:00:00Z"}]`
	script := fmt.Sprintf(`#!/bin/sh
printf 'beads_dir=%%s args=%%s\n' "${BEADS_DIR:-<unset>}" "$*" >> %q
if [ "${BEADS_DIR:-}" != %q ]; then echo '[]'; [ "$1" = "show" ] && exit 1; exit 0; fi
case "$1" in
  show|list) echo '%s' ;;
  *) echo '[]' ;;
esac
`, logPath, rigBeads, agent)
	bdPath = filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return townRoot, townBeads, rigBeads, logPath, bdPath
}

func readBDLog(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	return string(data)
}

func TestGetAgentBeadInfo_PolecatReadsOwningRigDB(t *testing.T) {
	townRoot, townBeads, rigBeads, logPath, bdPath := polecatLocalityTown(t)
	d := &Daemon{config: &Config{TownRoot: townRoot}, logger: log.New(&strings.Builder{}, "", 0), bdPath: bdPath}

	info, err := d.getAgentBeadInfo("gt-myr-polecat-mycat")
	if err != nil {
		t.Fatalf("getAgentBeadInfo: %v", err)
	}
	if info.State != "working" || info.HookBead != "gt-work" {
		t.Fatalf("getAgentBeadInfo = %+v, want the rig-local polecat bead", info)
	}
	if hook := d.getAgentHookBead("gt-myr-polecat-mycat"); hook != "gt-work" {
		t.Fatalf("getAgentHookBead = %q, want gt-work", hook)
	}
	log := readBDLog(t, logPath)
	if strings.Contains(log, "beads_dir="+townBeads+" ") || !strings.Contains(log, "beads_dir="+rigBeads+" args=show gt-myr-polecat-mycat") {
		t.Fatalf("polecat agent bead reads must target the rig DB; log:\n%s", log)
	}
}

// The GUPP and orphaned-work patrols list polecat agent beads from the rig
// database where every lifecycle path now writes them.
func TestRigPolecatPatrols_ListOwningRigDB(t *testing.T) {
	townRoot, townBeads, rigBeads, logPath, bdPath := polecatLocalityTown(t)
	tmuxDir := t.TempDir()
	writeFakeTestTmux(t, tmuxDir)
	t.Setenv("PATH", tmuxDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var logBuf strings.Builder
	d := &Daemon{config: &Config{TownRoot: townRoot}, logger: log.New(&logBuf, "", 0), tmux: tmux.NewTmux(), bdPath: bdPath}
	d.checkRigGUPPViolations("myr")
	d.checkRigOrphanedWork("myr")

	log := readBDLog(t, logPath)
	if strings.Contains(log, "beads_dir="+townBeads+" args=list") {
		t.Fatalf("polecat patrols listed the town DB; log:\n%s", log)
	}
	if got := strings.Count(log, "beads_dir="+rigBeads+" args=list --label=gt:agent"); got != 2 {
		t.Fatalf("expected both patrols to list the rig DB (got %d); log:\n%s\ndaemon log:\n%s", got, log, logBuf.String())
	}
}
