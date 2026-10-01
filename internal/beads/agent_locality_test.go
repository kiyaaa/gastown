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

// installMockBD installs a bd that logs BEADS_DIR + args. `show` succeeds in
// the databases listed in hasDirs and in any database a successful `create`
// has written to; `create` fails when createFails is set.
func (lt *localityTown) installMockBD(t *testing.T, hasDirs []string, createFails bool) {
	t.Helper()
	binDir := t.TempDir()
	lt.logPath = filepath.Join(binDir, "bd.log")
	createdPath := filepath.Join(binDir, "created")
	if lt.agentRecord == "" {
		lt.agentRecord = `[{"id":"gt-gastown-witness","title":"Witness for gastown","issue_type":"task","labels":["gt:agent"],"status":"open","description":"Witness for gastown\n\nrole_type: witness\nrig: gastown\nagent_state: running"}]`
	}
	createExit := 0
	if createFails {
		createExit = 1
	}
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
      if [ "${BEADS_DIR:-}" = "$d" ]; then
        printf '%%s\n' %q
        exit 0
      fi
    done
    if [ -f "$CREATED" ] && grep -Fxq "${BEADS_DIR:-}" "$CREATED"; then
      printf '%%s\n' %q
      exit 0
    fi
    echo "Error: no issue found" >&2
    exit 1
    ;;
  create)
    if [ %d -ne 0 ]; then echo 'already exists' >&2; exit 1; fi
    printf '%%s\n' "${BEADS_DIR:-}" >> "$CREATED"
    printf '{"id":"created","title":"t","status":"open"}\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, lt.logPath, createdPath, strings.Join(hasDirs, " "), lt.agentRecord, lt.agentRecord, createExit)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write mock bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (lt *localityTown) log(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(lt.logPath)
	if os.IsNotExist(err) {
		return "" // bd was never invoked
	}
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

// polecatRecord is a legacy polecat agent bead carrying lifecycle state that
// a migration must not drop: hook, active MR, a field this version does not
// parse, done-intent/checkpoint labels, and a safety stop.
const polecatRecord = `[{"id":"gt-gastown-polecat-rust","title":"Polecat rust","issue_type":"task","labels":["gt:agent","done-intent:COMPLETED:1700000000","done-cp:pushed:polecat/rust/gt-1:1700000001","safety_stop:gt-stop-1"],"status":"open","description":"Polecat rust\n\nrole_type: polecat\nrig: gastown\nagent_state: working\nhook_bead: gt-work-1\nactive_mr: gt-mr-1\nfuture_field: keep-me"}]`

func TestIsRigLocalAgentBeadID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gt-gastown-witness", true},
		{"gt-gastown-refinery", true},
		{"um-usage_monitor-witness", true},
		{"gt-gastown-crew-max", true},
		{"gt-gastown-polecat-rust", true}, // polecats are rig-local (gs-8hj)
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

// Creation from any wrapper (rig bootstrap, doctor, polecat spawn, or a
// ForAgentBead town wrapper such as gt done uses) must land rig-scoped beads,
// polecats included, in the rig DB.
func TestCreateAgentBead_RigScopedRolesUseOwningRigDB(t *testing.T) {
	for _, id := range []string{"gt-gastown-witness", "gt-gastown-refinery", "gt-gastown-crew-max", "gt-gastown-polecat-rust"} {
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

func TestCreateAgentBead_TownRolesStayInTownDB(t *testing.T) {
	tests := []struct {
		id   string
		role string
	}{
		{"hq-mayor", "mayor"},
		{"hq-deacon", "deacon"},
		{"hq-dog-alpha", "dog"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.installMockBD(t, nil, false)

			bd := NewWithBeadsDir(lt.rigDir, lt.rigBeads)
			if _, err := bd.CreateAgentBead(tt.id, tt.id, &AgentFields{RoleType: tt.role, AgentState: "idle"}); err != nil {
				t.Fatalf("CreateAgentBead: %v", err)
			}
			assertAllDirs(t, "create", lt.commandDirs(t, "create"), lt.townBeads)
		})
	}
}

// assertMigratedFromTown checks that a legacy town copy was migrated into the
// rig (created there with its description and labels) and that the town copy
// was retired by closing it, never updated in place.
func (lt *localityTown) assertMigratedFromTown(t *testing.T, id string) {
	t.Helper()
	assertAllDirs(t, "create", lt.commandDirs(t, "create"), lt.rigBeads)
	assertAllDirs(t, "close", lt.commandDirs(t, "close"), lt.townBeads)
	for _, dir := range lt.commandDirs(t, "update") {
		if dir == lt.townBeads {
			t.Fatalf("legacy town copy of %s was updated in place; log:\n%s", id, lt.log(t))
		}
	}
	log := lt.log(t)
	for _, want := range []string{
		"--id=" + id,
		"--title=Polecat rust",
		"hook_bead: gt-work-1",
		"active_mr: gt-mr-1",
		"future_field: keep-me",
		"--labels=gt:agent,done-intent:COMPLETED:1700000000,done-cp:pushed:polecat/rust/gt-1:1700000001,safety_stop:gt-stop-1",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("migration of %s lost %q; log:\n%s", id, want, log)
		}
	}
}

// Spawn (CreateOrReopen) of a polecat whose bead exists only as a legacy town
// copy migrates it into the rig and reuses it there, so labels such as safety
// stops survive and no fresh bead is created beside a stale town shadow.
func TestCreateOrReopenAgentBead_TownOnlyPolecatMigratesThenUpdatesRig(t *testing.T) {
	lt := newLocalityTown(t)
	lt.agentRecord = polecatRecord
	lt.installMockBD(t, []string{lt.townBeads}, false)

	bd := New(lt.root).ForAgentBead()
	if _, err := bd.CreateOrReopenAgentBead("gt-gastown-polecat-rust", "gt-gastown-polecat-rust", &AgentFields{RoleType: "polecat", Rig: "gastown", AgentState: "spawning"}); err != nil {
		t.Fatalf("CreateOrReopenAgentBead: %v", err)
	}
	lt.assertMigratedFromTown(t, "gt-gastown-polecat-rust")
}

func TestCreateOrReopenAgentBead_ExistingRigBeadUpdatedInRig(t *testing.T) {
	for _, id := range []string{"gt-gastown-refinery", "gt-gastown-polecat-rust"} {
		t.Run(id, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.installMockBD(t, []string{lt.rigBeads, lt.townBeads}, true)

			bd := New(lt.root).ForAgentBead()
			_, role, _, _ := ParseAgentBeadID(id)
			if _, err := bd.CreateOrReopenAgentBead(id, id, &AgentFields{RoleType: role, Rig: "gastown", AgentState: "idle"}); err != nil {
				t.Fatalf("CreateOrReopenAgentBead: %v", err)
			}
			assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.rigBeads)
			assertAllDirs(t, "show", lt.commandDirs(t, "show"), lt.rigBeads)
		})
	}
}

// Duplicates resolve deterministically: with copies in both databases every
// lifecycle write (state, done completion, active_mr, nuke reset) hits the
// rig copy and never the town shadow.
func TestAgentWrites_DuplicatePrefersRigCopy(t *testing.T) {
	writes := map[string]func(*Beads, string) error{
		"state": func(b *Beads, id string) error { return b.UpdateAgentState(id, "working") },
		"completion": func(b *Beads, id string) error {
			return b.UpdateAgentCompletion(id, &CompletionMetadata{ExitType: "COMPLETED", MRID: "gt-mr-2"})
		},
		"active_mr": func(b *Beads, id string) error { return b.UpdateAgentActiveMR(id, "gt-mr-2") },
		"reset":     func(b *Beads, id string) error { return b.ResetAgentBeadForReuse(id, "nuked") },
		"clear_mr": func(b *Beads, id string) error {
			_, err := b.ClearAgentActiveMRIfMatches(id, "gt-mr-1")
			return err
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.agentRecord = polecatRecord
			lt.installMockBD(t, []string{lt.rigBeads, lt.townBeads}, false)

			if err := write(New(lt.root).ForAgentBead(), "gt-gastown-polecat-rust"); err != nil {
				t.Fatalf("write: %v", err)
			}
			assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.rigBeads)
			assertAllDirs(t, "show", lt.commandDirs(t, "show"), lt.rigBeads)
			if creates := lt.commandDirs(t, "create"); len(creates) != 0 {
				t.Fatalf("write created a bead although the rig copy exists: %v", creates)
			}
		})
	}
}

// Un-repaired towns converge on the rig copy: a write to a bead that exists
// only as a legacy town copy migrates it first, then writes the rig copy.
func TestAgentWrites_TownOnlyMigratesThenWritesRig(t *testing.T) {
	for _, wrapper := range []string{"rig", "town-for-agent"} {
		t.Run(wrapper, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.agentRecord = polecatRecord
			lt.installMockBD(t, []string{lt.townBeads}, false)

			bd := NewWithBeadsDir(lt.rigDir, lt.rigBeads)
			if wrapper == "town-for-agent" {
				bd = New(lt.root).ForAgentBead()
			}
			if err := bd.UpdateAgentState("gt-gastown-polecat-rust", "done"); err != nil {
				t.Fatalf("UpdateAgentState: %v", err)
			}
			lt.assertMigratedFromTown(t, "gt-gastown-polecat-rust")
			assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.rigBeads)
		})
	}
}

// Reads of an unmigrated legacy bead still see it, without mutating anything.
func TestGetAgentBead_TownOnlyReadsLegacyWithoutMigrating(t *testing.T) {
	lt := newLocalityTown(t)
	lt.agentRecord = polecatRecord
	lt.installMockBD(t, []string{lt.townBeads}, false)

	issue, fields, err := NewWithBeadsDir(lt.rigDir, lt.rigBeads).GetAgentBead("gt-gastown-polecat-rust")
	if err != nil || issue == nil || fields == nil {
		t.Fatalf("GetAgentBead = (%v, %v, %v), want the legacy town copy", issue, fields, err)
	}
	if fields.ActiveMR != "gt-mr-1" || fields.HookBead != "gt-work-1" {
		t.Fatalf("GetAgentBead fields = %+v, want legacy hook/active_mr", fields)
	}
	for _, cmd := range []string{"create", "update", "close"} {
		if dirs := lt.commandDirs(t, cmd); len(dirs) != 0 {
			t.Fatalf("read issued %s in %v; log:\n%s", cmd, dirs, lt.log(t))
		}
	}
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

// gt done's done-intent labels and checkpoints use generic Update/Show on the
// wrapper from ForAgentBeadID; for polecats that must be the rig copy, and
// town roles keep using the town DB.
func TestForAgentBeadID_BindsGenericCallsToHome(t *testing.T) {
	tests := []struct {
		id      string
		wantDir func(*localityTown) string
	}{
		{"gt-gastown-polecat-rust", func(lt *localityTown) string { return lt.rigBeads }},
		{"gt-gastown-witness", func(lt *localityTown) string { return lt.rigBeads }},
		{"hq-mayor", func(lt *localityTown) string { return lt.townBeads }},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.installMockBD(t, []string{lt.rigBeads, lt.townBeads}, false)

			bd := New(lt.rigDir).ForAgentBeadID(tt.id)
			if err := bd.Update(tt.id, UpdateOptions{AddLabels: []string{"done-cp:pushed:b:1"}}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if _, err := bd.Show(tt.id); err != nil {
				t.Fatalf("Show: %v", err)
			}
			assertAllDirs(t, "update", lt.commandDirs(t, "update"), tt.wantDir(lt))
		})
	}
}

func TestForAgentBeadID_TownOnlyPolecatIsMigratedFirst(t *testing.T) {
	lt := newLocalityTown(t)
	lt.agentRecord = polecatRecord
	lt.installMockBD(t, []string{lt.townBeads}, false)

	bd := New(lt.root).ForAgentBeadID("gt-gastown-polecat-rust")
	if err := bd.Update("gt-gastown-polecat-rust", UpdateOptions{AddLabels: []string{"done-cp:mr-created:gt-mr-2:2"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	lt.assertMigratedFromTown(t, "gt-gastown-polecat-rust")
	assertAllDirs(t, "update", lt.commandDirs(t, "update"), lt.rigBeads)
}

func TestMigrateLegacyAgentBead_NoOps(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		hasDirs func(*localityTown) []string
	}{
		{"rig copy exists", "gt-gastown-polecat-rust", func(lt *localityTown) []string { return []string{lt.rigBeads, lt.townBeads} }},
		{"missing everywhere", "gt-gastown-polecat-rust", func(*localityTown) []string { return nil }},
		{"town role", "hq-mayor", func(lt *localityTown) []string { return []string{lt.townBeads} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt := newLocalityTown(t)
			lt.installMockBD(t, tt.hasDirs(lt), false)

			migrated, err := New(lt.root).MigrateLegacyAgentBead(tt.id)
			if err != nil || migrated {
				t.Fatalf("MigrateLegacyAgentBead = (%v, %v), want (false, nil)", migrated, err)
			}
			for _, cmd := range []string{"create", "update", "close"} {
				if dirs := lt.commandDirs(t, cmd); len(dirs) != 0 {
					t.Fatalf("no-op migration issued %s in %v", cmd, dirs)
				}
			}
		})
	}
}

// A non-open legacy status (e.g. a parked polecat) is restored on the rig copy.
func TestMigrateLegacyAgentBead_PreservesStatus(t *testing.T) {
	lt := newLocalityTown(t)
	lt.agentRecord = strings.Replace(polecatRecord, `"status":"open"`, `"status":"in_progress"`, 1)
	lt.installMockBD(t, []string{lt.townBeads}, false)

	migrated, err := New(lt.root).MigrateLegacyAgentBead("gt-gastown-polecat-rust")
	if err != nil || !migrated {
		t.Fatalf("MigrateLegacyAgentBead = (%v, %v), want (true, nil)", migrated, err)
	}
	lt.assertMigratedFromTown(t, "gt-gastown-polecat-rust")
	if !strings.Contains(lt.log(t), "beads_dir="+lt.rigBeads+" args=update gt-gastown-polecat-rust --status=in_progress") {
		t.Fatalf("status not restored on rig copy; log:\n%s", lt.log(t))
	}
}

func TestMigrateLegacyAgentBead_CarriesColumnOnlyAgentState(t *testing.T) {
	issue := &Issue{Description: "Polecat rust\n\nrole_type: polecat", AgentState: "working"}
	got := legacyAgentDescription(issue)
	if fields := ParseAgentFields(got); fields == nil || fields.AgentState != "working" || fields.RoleType != "polecat" {
		t.Fatalf("legacyAgentDescription = %q, want agent_state carried from column", got)
	}
	issue.Description += "\nagent_state: done"
	if got := legacyAgentDescription(issue); got != issue.Description {
		t.Fatalf("legacyAgentDescription rewrote a description that already has agent_state: %q", got)
	}
}

func TestRetireLegacyAgentBeadShadow(t *testing.T) {
	t.Run("duplicate retires town copy", func(t *testing.T) {
		lt := newLocalityTown(t)
		lt.agentRecord = polecatRecord
		lt.installMockBD(t, []string{lt.rigBeads, lt.townBeads}, false)

		retired, err := New(lt.root).RetireLegacyAgentBeadShadow("gt-gastown-polecat-rust")
		if err != nil || !retired {
			t.Fatalf("RetireLegacyAgentBeadShadow = (%v, %v), want (true, nil)", retired, err)
		}
		assertAllDirs(t, "close", lt.commandDirs(t, "close"), lt.townBeads)
		if dirs := lt.commandDirs(t, "update"); len(dirs) != 0 {
			t.Fatalf("retire updated a bead: %v", dirs)
		}
	})
	t.Run("town-only copy is never retired", func(t *testing.T) {
		lt := newLocalityTown(t)
		lt.agentRecord = polecatRecord
		lt.installMockBD(t, []string{lt.townBeads}, false)

		retired, err := New(lt.root).RetireLegacyAgentBeadShadow("gt-gastown-polecat-rust")
		if err != nil || retired {
			t.Fatalf("RetireLegacyAgentBeadShadow = (%v, %v), want (false, nil)", retired, err)
		}
		if dirs := lt.commandDirs(t, "close"); len(dirs) != 0 {
			t.Fatalf("retired the only copy: %v", dirs)
		}
	})
}

func TestAgentBeadHomeDir(t *testing.T) {
	lt := newLocalityTown(t)
	for id, want := range map[string]string{
		"gt-gastown-polecat-rust": lt.rigBeads,
		"gt-gastown-witness":      lt.rigBeads,
		"hq-mayor":                lt.townBeads,
		"zz-unrouted-polecat-x":   lt.townBeads,
	} {
		if got := AgentBeadHomeDir(lt.root, id); got != want {
			t.Errorf("AgentBeadHomeDir(%q) = %s, want %s", id, got, want)
		}
	}
	if got := RigAgentBeadsDir(lt.root, "gt"); got != lt.rigBeads {
		t.Errorf("RigAgentBeadsDir(gt) = %s, want %s", got, lt.rigBeads)
	}
	if got := RigAgentBeadsDir(lt.root, "zz"); got != lt.townBeads {
		t.Errorf("RigAgentBeadsDir(zz) = %s, want town %s", got, lt.townBeads)
	}
}

// gt polecat list, scheduler capacity, and polecat identity list read polecat
// beads with beads.New(<rig path>).ListAgentBeads(), where <rig path> holds a
// .beads redirect to mayor/rig/.beads. Every lifecycle write must resolve to
// that same database, so list/capacity see what create/done/post-merge wrote
// without manual copies (gs-8hj).
func TestPolecatLifecycleWritesTheDatabaseRigListingsRead(t *testing.T) {
	lt := newLocalityTown(t)
	rigPath := filepath.Join(lt.root, "gastown")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigPath, ".beads", "redirect"), []byte("mayor/rig/.beads\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lt.agentRecord = polecatRecord
	lt.installMockBD(t, []string{lt.rigBeads}, false)

	const id = "gt-gastown-polecat-rust"
	_, _ = New(rigPath).ListAgentBeads() // only the database it queries matters
	listDirs := lt.commandDirs(t, "list")
	if len(listDirs) == 0 {
		t.Fatalf("ListAgentBeads issued no bd list; log:\n%s", lt.log(t))
	}

	town := New(lt.root).ForAgentBead()
	if _, err := town.CreateOrReopenAgentBead(id, id, &AgentFields{RoleType: "polecat", Rig: "gastown", AgentState: "spawning"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := town.UpdateAgentCompletion(id, &CompletionMetadata{ExitType: "COMPLETED", MRID: "gt-mr-1"}); err != nil {
		t.Fatalf("done completion: %v", err)
	}
	if _, err := New(lt.rigDir).ForAgentBeadID(id).ClearAgentActiveMRIfMatches(id, "gt-mr-1"); err != nil {
		t.Fatalf("post-merge clear: %v", err)
	}

	listDir := listDirs[0]
	assertAllDirs(t, "list", listDirs, listDir)
	assertAllDirs(t, "update", lt.commandDirs(t, "update"), listDir)
	if listDir != lt.rigBeads || AgentBeadHomeDir(lt.root, id) != listDir {
		t.Fatalf("rig listing reads %s but the polecat home is %s", listDir, AgentBeadHomeDir(lt.root, id))
	}
}
