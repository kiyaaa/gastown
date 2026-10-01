package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// doneLocalityTown is a town with rig "gastown" (prefix gt-, beads at
// gastown/mayor/rig/.beads) and a stateful bd mock: `show` finds the agent
// bead in the databases listed at install time and in any database a `create`
// has written to.
type doneLocalityTown struct {
	root, townBeads, rigDir, rigBeads, logPath string
}

// doneLegacyPolecatRecord is a town-only polecat bead left by an in-flight
// gt done that started before polecat beads moved to the rig DB (gs-8hj).
const doneLegacyPolecatRecord = `[{"id":"gt-gastown-polecat-rust","title":"Polecat rust","issue_type":"task","labels":["gt:agent","done-intent:COMPLETED:1700000000","done-cp:pushed:polecat/rust/gt-1:1700000001"],"status":"open","description":"Polecat rust\n\nrole_type: polecat\nrig: gastown\nagent_state: working\nhook_bead: gt-work-1\ncleanup_status: clean"}]`

func newDoneLocalityTown(t *testing.T, hasDirs func(*doneLocalityTown) []string) *doneLocalityTown {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix shell script mock for bd")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	lt := &doneLocalityTown{
		root:      root,
		townBeads: filepath.Join(root, ".beads"),
		rigDir:    filepath.Join(root, "gastown", "mayor", "rig"),
	}
	lt.rigBeads = filepath.Join(lt.rigDir, ".beads")
	for _, dir := range []string{filepath.Join(root, "mayor"), lt.townBeads, lt.rigBeads} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := beads.WriteRoutes(lt.townBeads, []beads.Route{{Prefix: "hq-", Path: "."}, {Prefix: "gt-", Path: "gastown/mayor/rig"}}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{lt.townBeads, lt.rigBeads} {
		if err := os.WriteFile(filepath.Join(dir, ".gt-types-configured"), []byte(beads.TypeConfigSentinelValue()+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	binDir := t.TempDir()
	lt.logPath = filepath.Join(binDir, "bd.log")
	script := fmt.Sprintf(`#!/bin/sh
LOG=%q
CREATED=%q
printf 'beads_dir=%%s args=%%s\n' "${BEADS_DIR:-<unset>}" "$*" >> "$LOG"
cmd=""
for arg in "$@"; do
  case "$arg" in
    --*) ;;
    *) cmd="$arg"; break ;;
  esac
done
case "$cmd" in
  show)
    for d in %s; do
      if [ "${BEADS_DIR:-}" = "$d" ]; then printf '%%s\n' %q; exit 0; fi
    done
    if [ -f "$CREATED" ] && grep -Fxq "${BEADS_DIR:-}" "$CREATED"; then printf '%%s\n' %q; exit 0; fi
    echo "Error: no issue found" >&2
    exit 1
    ;;
  create)
    printf '%%s\n' "${BEADS_DIR:-}" >> "$CREATED"
    printf '{"id":"gt-gastown-polecat-rust","title":"t","status":"open"}\n'
    ;;
  *)
    exit 0
    ;;
esac
`, lt.logPath, filepath.Join(binDir, "created"), strings.Join(hasDirs(lt), " "), doneLegacyPolecatRecord, doneLegacyPolecatRecord)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return lt
}

func (lt *doneLocalityTown) log(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(lt.logPath)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	return string(data)
}

// An in-flight gt done whose polecat bead exists only as a legacy town copy
// migrates it into the rig DB (hook, cleanup status, done-intent and
// checkpoint labels intact) instead of recreating a blank idle bead, and then
// resumes from the migrated checkpoints and writes new ones to the rig copy.
func TestDoneAgentBead_LegacyTownCopyMigratedAndResumed(t *testing.T) {
	lt := newDoneLocalityTown(t, func(lt *doneLocalityTown) []string { return []string{lt.townBeads} })
	const id = "gt-gastown-polecat-rust"
	ctx := RoleContext{Role: RolePolecat, Rig: "gastown", Polecat: "rust", TownRoot: lt.root}

	ensureAgentBeadExists(beads.New(lt.rigDir).ForAgentBead(), id, ctx)

	log := lt.log(t)
	for _, want := range []string{
		"beads_dir=" + lt.rigBeads + " args=create --json --id=" + id,
		"hook_bead: gt-work-1",
		"cleanup_status: clean",
		"--labels=gt:agent,done-intent:COMPLETED:1700000000,done-cp:pushed:polecat/rust/gt-1:1700000001",
		"beads_dir=" + lt.townBeads + " args=close " + id,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("legacy polecat bead not migrated intact (missing %q); log:\n%s", want, log)
		}
	}
	if strings.Contains(log, "agent_state: idle") {
		t.Fatalf("gt done recreated a blank idle bead instead of migrating; log:\n%s", log)
	}

	agentBd := beads.New(lt.rigDir).ForAgentBeadID(id)
	if cps := readDoneCheckpoints(agentBd, id); cps[CheckpointPushed] != "polecat/rust/gt-1" {
		t.Fatalf("readDoneCheckpoints after migration = %v, want pushed checkpoint", cps)
	}
	writeDoneCheckpoint(agentBd, id, CheckpointMRCreated, "gt-mr-1")
	if err := agentBd.UpdateAgentActiveMR(id, "gt-mr-1"); err != nil {
		t.Fatalf("UpdateAgentActiveMR: %v", err)
	}

	for _, line := range strings.Split(lt.log(t), "\n") {
		if strings.Contains(line, "args=update "+id) && !strings.HasPrefix(line, "beads_dir="+lt.rigBeads+" ") {
			t.Fatalf("gt done wrote the agent bead outside the rig DB: %s", line)
		}
	}
	if !strings.Contains(lt.log(t), "beads_dir="+lt.rigBeads+" args=update "+id+" --add-label=done-cp:mr-created:gt-mr-1:") {
		t.Fatalf("mr-created checkpoint not written to the rig copy; log:\n%s", lt.log(t))
	}
}

// With a rig-local bead already present, gt done neither recreates nor
// migrates anything, and a stale town duplicate is left alone by the hot path.
func TestDoneAgentBead_RigCopyPresentIsUsedAsIs(t *testing.T) {
	lt := newDoneLocalityTown(t, func(lt *doneLocalityTown) []string { return []string{lt.rigBeads, lt.townBeads} })
	const id = "gt-gastown-polecat-rust"
	ctx := RoleContext{Role: RolePolecat, Rig: "gastown", Polecat: "rust", TownRoot: lt.root}

	ensureAgentBeadExists(beads.New(lt.rigDir).ForAgentBead(), id, ctx)
	setDoneIntentLabel(beads.New(lt.rigDir).ForAgentBeadID(id), id, "COMPLETED")

	log := lt.log(t)
	for _, cmd := range []string{"create", "close"} {
		if strings.Contains(log, "args="+cmd+" ") {
			t.Fatalf("gt done issued %s although the rig copy exists; log:\n%s", cmd, log)
		}
	}
	if !strings.Contains(log, "beads_dir="+lt.rigBeads+" args=update "+id+" --add-label=done-intent:COMPLETED:") {
		t.Fatalf("done-intent label not written to the rig copy; log:\n%s", log)
	}
	if strings.Contains(log, "beads_dir="+lt.townBeads+" args=update") {
		t.Fatalf("gt done wrote the town duplicate; log:\n%s", log)
	}
}

// gt status merges town and rig agent listings concurrently; the rig-local
// copy of a polecat bead must win regardless of arrival order, while a
// town-only legacy copy is still shown and town roles come from town.
func TestMergeStatusAgentBeads_RigCopyWinsInAnyOrder(t *testing.T) {
	town := map[string]*beads.Issue{
		"gt-gastown-polecat-rust": {ID: "gt-gastown-polecat-rust", Description: "agent_state: working\nactive_mr: gt-stale"},
		"gt-gastown-polecat-fury": {ID: "gt-gastown-polecat-fury", Description: "agent_state: idle"},
		"hq-mayor":                {ID: "hq-mayor", Description: "town"},
	}
	rig := map[string]*beads.Issue{
		"gt-gastown-polecat-rust": {ID: "gt-gastown-polecat-rust", Description: "agent_state: done"},
	}
	for _, townFirst := range []bool{true, false} {
		merged := map[string]*beads.Issue{}
		if townFirst {
			mergeStatusAgentBeads(merged, town, true)
			mergeStatusAgentBeads(merged, rig, false)
		} else {
			mergeStatusAgentBeads(merged, rig, false)
			mergeStatusAgentBeads(merged, town, true)
		}
		if got := merged["gt-gastown-polecat-rust"].Description; got != "agent_state: done" {
			t.Errorf("townFirst=%v: duplicate resolved to %q, want the rig copy", townFirst, got)
		}
		if merged["gt-gastown-polecat-fury"] == nil || merged["hq-mayor"] == nil {
			t.Errorf("townFirst=%v: town-only beads dropped: %v", townFirst, merged)
		}
	}
}
