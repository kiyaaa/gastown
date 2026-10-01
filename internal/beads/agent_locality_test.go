package beads

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// localityTown is a fake town with one rig ("gastown", prefix "gt-") whose
// beads live at gastown/mayor/rig/.beads.
type localityTown struct {
	root        string
	townBeads   string
	rigDir      string
	rigBeads    string
	logPath     string
	agentRecord string // JSON returned by mock `bd show`
}

func newLocalityTown(t *testing.T) *localityTown {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix shell script mock for bd")
	}

	root, _ := filepath.EvalSymlinks(t.TempDir())
	lt := &localityTown{
		root:      root,
		townBeads: filepath.Join(root, ".beads"),
		rigDir:    filepath.Join(root, "gastown", "mayor", "rig"),
	}
	lt.rigBeads = filepath.Join(lt.rigDir, ".beads")
	for _, dir := range []string{filepath.Join(root, "mayor"), lt.townBeads, lt.rigBeads} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteRoutes(lt.townBeads, []Route{{Prefix: "hq-", Path: "."}, {Prefix: "gt-", Path: "gastown/mayor/rig"}}); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	for _, dir := range []string{lt.townBeads, lt.rigBeads} {
		if err := os.WriteFile(filepath.Join(dir, ".gt-types-configured"), []byte(TypeConfigSentinelValue()+"\n"), 0644); err != nil {
			t.Fatalf("write types sentinel: %v", err)
		}
	}
	return lt
}

// installMockBD installs a bd that logs BEADS_DIR + args. `show` succeeds only
// in the databases listed in hasDirs; `create` fails when createFails is set.
func (lt *localityTown) installMockBD(t *testing.T, hasDirs []string, createFails bool) {
	t.Helper()
	binDir := t.TempDir()
	lt.logPath = filepath.Join(binDir, "bd.log")
	if lt.agentRecord == "" {
		lt.agentRecord = `[{"id":"gt-gastown-witness","title":"Witness for gastown","issue_type":"task","labels":["gt:agent"],"status":"open","description":"Witness for gastown\n\nrole_type: witness\nrig: gastown\nagent_state: running"}]`
	}
	createExit := 0
	if createFails {
		createExit = 1
	}
	script := fmt.Sprintf(`#!/bin/sh
LOG=%q
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
      if [ "${BEADS_DIR:-}" = "$d" ]; then
        printf '%%s\n' %q
        exit 0
      fi
    done
    echo "Error: no issue found" >&2
    exit 1
    ;;
  create)
    if [ %d -ne 0 ]; then echo 'already exists' >&2; exit 1; fi
    printf '{"id":"created","title":"t","status":"open"}\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, lt.logPath, strings.Join(hasDirs, " "), lt.agentRecord, createExit)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write mock bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (lt *localityTown) log(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(lt.logPath)
	if err != nil {
		t.Fatalf("read mock bd log: %v", err)
	}
	return string(data)
}

// commandDirs returns the BEADS_DIR of every logged invocation of cmd.
func (lt *localityTown) commandDirs(t *testing.T, cmd string) []string {
	t.Helper()
	var dirs []string
	for _, line := range strings.Split(lt.log(t), "\n") {
		dir, args, ok := strings.Cut(strings.TrimPrefix(line, "beads_dir="), " args=")
		if ok && strings.HasPrefix(args, cmd+" ") {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func assertAllDirs(t *testing.T, what string, got []string, want string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: no invocations recorded", what)
	}
	for _, dir := range got {
		if dir != want {
			t.Fatalf("%s used BEADS_DIR %s, want %s (all: %v)", what, dir, want, got)
		}
	}
}

func TestIsRigLocalAgentBeadID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gt-gastown-witness", true},
		{"gt-gastown-refinery", true},
		{"um-usage_monitor-witness", true},
		{"gt-gastown-crew-max", true},
		{"gt-gastown-polecat-rust", false}, // polecats are town-owned (see gs-8hj)
		{"hq-mayor", false},
		{"hq-deacon", false},
		{"hq-dog-alpha", false},
		{"gt-abc123", false},
	}
	for _, tt := range tests {
		if got := IsRigLocalAgentBeadID(tt.id); got != tt.want {
			t.Errorf("IsRigLocalAgentBeadID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

// Creation from any wrapper (rig bootstrap, doctor, or a ForAgentBead town
// wrapper such as gt done uses) must land rig-scoped beads in the rig DB.
func TestCreateAgentBead_RigScopedRolesUseOwningRigDB(t *testing.T) {
	for _, id := range []string{"gt-gastown-witness", "gt-gastown-refinery", "gt-gastown-crew-max"} {
		for _, wrapper := range []string{"rig", "town-for-agent"} {
			t.Run(id+"/"+wrapper, func(t *testing.T) {
				lt := newLocalityTown(t)
				lt.installMockBD(t, nil, false)

				bd := NewWithBeadsDir(lt.rigDir, lt.rigBeads)
				if wrapper == "town-for-agent" {
					bd = New(lt.root).ForAgentBead()
				}
				_, role, _, _ := ParseAgentBeadID(id)
				if _, err := bd.CreateAgentBead(id, id, &AgentFields{RoleType: role, Rig: "gastown", AgentState: "idle"}); err != nil {
					t.Fatalf("CreateAgentBead: %v", err)
				}
				assertAllDirs(t, "create", lt.commandDirs(t, "create"), lt.rigBeads)
			})
		}
	}
}

func TestCreateAgentBead_PolecatAndTownRolesStayInTownDB(t *testing.T) {
	tests := []struct {
		id   string
		role string
		rig  string
	}{
		{"gt-gastown-polecat-rust", "polecat", "gastown"},
		{"hq-mayor", "mayor", ""},
		{"hq-deacon", "deacon", ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.installMockBD(t, nil, false)

			bd := NewWithBeadsDir(lt.rigDir, lt.rigBeads)
			if _, err := bd.CreateAgentBead(tt.id, tt.id, &AgentFields{RoleType: tt.role, Rig: tt.rig, AgentState: "idle"}); err != nil {
				t.Fatalf("CreateAgentBead: %v", err)
			}
			assertAllDirs(t, "create", lt.commandDirs(t, "create"), lt.townBeads)
		})
	}
}

// A rig-scoped bead that exists only as a legacy town copy is repaired by
// CreateOrReopen: the rig-local bead is created and the town copy untouched.
func TestCreateOrReopenAgentBead_TownOnlyRigBeadCreatesRigLocal(t *testing.T) {
	lt := newLocalityTown(t)
	lt.installMockBD(t, []string{lt.townBeads}, false)

	bd := New(lt.root).ForAgentBead()
	if _, err := bd.CreateOrReopenAgentBead("gt-gastown-witness", "Witness for gastown", &AgentFields{RoleType: "witness", Rig: "gastown", AgentState: "idle"}); err != nil {
		t.Fatalf("CreateOrReopenAgentBead: %v", err)
	}
	assertAllDirs(t, "create", lt.commandDirs(t, "create"), lt.rigBeads)
	if updates := lt.commandDirs(t, "update"); len(updates) != 0 {
		t.Fatalf("CreateOrReopenAgentBead updated an existing bead (%v); log:\n%s", updates, lt.log(t))
	}
}

func TestCreateOrReopenAgentBead_ExistingRigBeadUpdatedInRig(t *testing.T) {
	lt := newLocalityTown(t)
	lt.installMockBD(t, []string{lt.rigBeads, lt.townBeads}, true)

	bd := New(lt.root).ForAgentBead()
	if _, err := bd.CreateOrReopenAgentBead("gt-gastown-refinery", "Refinery for gastown", &AgentFields{RoleType: "refinery", Rig: "gastown", AgentState: "idle"}); err != nil {
		t.Fatalf("CreateOrReopenAgentBead: %v", err)
	}
	assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.rigBeads)
	assertAllDirs(t, "show", lt.commandDirs(t, "show"), lt.rigBeads)
}

func TestUpdateAgentState_RigScopedPrefersRigCopy(t *testing.T) {
	lt := newLocalityTown(t)
	lt.installMockBD(t, []string{lt.rigBeads, lt.townBeads}, false)

	if err := New(lt.root).ForAgentBead().UpdateAgentState("gt-gastown-witness", "running"); err != nil {
		t.Fatalf("UpdateAgentState: %v", err)
	}
	assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.rigBeads)
}

// Un-repaired towns keep working: with no rig copy, updates reach the legacy
// town copy instead of failing with "issue not found".
func TestUpdateAgentState_RigScopedFallsBackToLegacyTownCopy(t *testing.T) {
	lt := newLocalityTown(t)
	lt.installMockBD(t, []string{lt.townBeads}, false)

	bd := NewWithBeadsDir(lt.rigDir, lt.rigBeads)
	if err := bd.UpdateAgentState("gt-gastown-witness", "running"); err != nil {
		t.Fatalf("UpdateAgentState: %v", err)
	}
	assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.townBeads)
}

func TestGetAgentBead_RigScopedMissingEverywhereReturnsNil(t *testing.T) {
	lt := newLocalityTown(t)
	lt.installMockBD(t, nil, false)

	issue, fields, err := NewWithBeadsDir(lt.rigDir, lt.rigBeads).GetAgentBead("gt-gastown-refinery")
	if err != nil || issue != nil || fields != nil {
		t.Fatalf("GetAgentBead = (%v, %v, %v), want nil, nil, nil", issue, fields, err)
	}
	shows := lt.commandDirs(t, "show")
	if len(shows) == 0 || shows[len(shows)-1] != lt.rigBeads {
		t.Fatalf("final show should target rig home %s; got %v", lt.rigBeads, shows)
	}
}

func TestShowAgentBeadAtHome_DoesNotFallBackToTown(t *testing.T) {
	lt := newLocalityTown(t)
	lt.installMockBD(t, []string{lt.townBeads}, false)

	if _, err := New(lt.root).ForAgentBead().ShowAgentBeadAtHome("gt-gastown-witness"); err == nil {
		t.Fatal("ShowAgentBeadAtHome found the town-only witness; want not found in rig home")
	}
	assertAllDirs(t, "show", lt.commandDirs(t, "show"), lt.rigBeads)
}
