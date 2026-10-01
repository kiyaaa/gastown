package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

func TestAgentBeadInventoryLookup(t *testing.T) {
	inv := newAgentBeadInventory()
	inv.townIssues["gs-gastown-witness"] = &beads.Issue{ID: "gs-gastown-witness"}
	inv.townWisps["gs-gastown-crew-max"] = true
	inv.townIssues["gs-gastown-polecat-rust"] = &beads.Issue{ID: "gs-gastown-polecat-rust"}
	inv.townIssues["gs-gastown-polecat-fury"] = &beads.Issue{ID: "gs-gastown-polecat-fury"}
	inv.rigIssues["gs-gastown-polecat-fury"] = &beads.Issue{ID: "gs-gastown-polecat-fury", Labels: []string{"gt:agent"}}
	inv.townIssues["hq-mayor"] = &beads.Issue{ID: "hq-mayor"}
	inv.townIssues["gs-gastown-refinery"] = &beads.Issue{ID: "gs-gastown-refinery"}
	inv.rigIssues["gs-gastown-refinery"] = &beads.Issue{ID: "gs-gastown-refinery", Labels: []string{"gt:agent"}}

	tests := []struct {
		id        string
		want      agentBeadPresence
		wantIssue bool
	}{
		{"gs-gastown-witness", agentBeadTownOnly, true},      // rig-scoped, town copy only
		{"gs-gastown-crew-max", agentBeadTownOnly, false},    // rig-scoped, town wisp only
		{"gs-gastown-refinery", agentBeadPresent, true},      // duplicate: rig copy wins
		{"gs-gastown-polecat-rust", agentBeadTownOnly, true}, // polecats are rig-local (gs-8hj)
		{"gs-gastown-polecat-fury", agentBeadPresent, true},  // duplicate: rig copy wins
		{"hq-mayor", agentBeadPresent, true},                 // town role stays in hq
		{"gs-gastown-crew-nobody", agentBeadMissing, false},
	}
	for _, tt := range tests {
		got, issue := inv.lookup(tt.id)
		if got != tt.want || (issue != nil) != tt.wantIssue {
			t.Errorf("lookup(%q) = (%v, issue=%v), want (%v, issue=%v)", tt.id, got, issue != nil, tt.want, tt.wantIssue)
		}
	}
	if _, issue := inv.lookup("gs-gastown-refinery"); !beads.HasLabel(issue, "gt:agent") {
		t.Errorf("lookup returned the town duplicate instead of the rig-local refinery bead")
	}

	for id, want := range map[string]bool{
		"gs-gastown-refinery":     true,  // rig copy + town duplicate
		"gs-gastown-polecat-fury": true,  // rig copy + town duplicate
		"gs-gastown-witness":      false, // town-only: misplaced, not shadowed
		"gs-gastown-polecat-rust": false, // town-only
		"hq-mayor":                false, // town role
	} {
		if got := inv.hasTownShadow(id); got != want {
			t.Errorf("hasTownShadow(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestAgentBeadsCheckResult_MisplacedIsFixableWarning(t *testing.T) {
	c := NewAgentBeadsCheck()
	res := c.result(4, nil, []string{"gs-gastown-witness"}, nil, nil)
	if res.Status != StatusWarning {
		t.Fatalf("status = %v, want warning", res.Status)
	}
	if !strings.Contains(res.Message, "only in town beads") || len(res.Details) != 1 || !strings.Contains(res.Details[0], "gs-gastown-witness") {
		t.Fatalf("result = %+v, want misplaced witness diagnostic", res)
	}

	if res := c.result(4, []string{"gs-gastown-refinery"}, []string{"gs-gastown-witness"}, nil, nil); res.Status != StatusError {
		t.Fatalf("missing bead status = %v, want error", res.Status)
	}
	if res := c.result(4, nil, nil, nil, nil); res.Status != StatusOK {
		t.Fatalf("clean status = %v, want OK", res.Status)
	}
	res = c.result(4, nil, nil, []string{"gs-gastown-polecat-fury"}, nil)
	if res.Status != StatusWarning || !strings.Contains(res.Message, "stale town duplicate") || len(res.Details) != 1 || !strings.Contains(res.Details[0], "gs-gastown-polecat-fury") {
		t.Fatalf("shadowed result = %+v, want stale town duplicate warning", res)
	}
}

// townOnlyAgentTown builds a town whose witness, refinery, and polecat rust
// beads exist only in the town database (the hq-ou3 / gs-8hj legacy state),
// and whose polecat fury has a rig-local bead plus a stale town duplicate.
func townOnlyAgentTown(t *testing.T) (townRoot, rigBeads, logFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses bash mock for bd")
	}

	townRoot, _ = filepath.EvalSymlinks(t.TempDir())
	townBeads := filepath.Join(townRoot, ".beads")
	rigBeads = filepath.Join(townRoot, "gastown", "mayor", "rig", ".beads")
	for _, dir := range []string{
		filepath.Join(townRoot, "mayor"),
		townBeads,
		rigBeads,
		filepath.Join(townRoot, "gastown", "polecats", "rust", ".git"),
		filepath.Join(townRoot, "gastown", "polecats", "fury", ".git"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	routes := `{"prefix":"hq-","path":"."}` + "\n" + `{"prefix":"gs-","path":"gastown/mayor/rig"}` + "\n"
	if err := os.WriteFile(filepath.Join(townBeads, "routes.jsonl"), []byte(routes), 0644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{townBeads, rigBeads} {
		if err := os.WriteFile(filepath.Join(dir, ".gt-types-configured"), []byte(beads.TypeConfigSentinelValue()+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	agent := func(id, role, state string, labels ...string) string {
		labelJSON := `"gt:agent"`
		for _, l := range labels {
			labelJSON += fmt.Sprintf(`,%q`, l)
		}
		return fmt.Sprintf(`{"id":"%s","title":"%s title","issue_type":"task","status":"open","labels":[%s],"description":"%s title\n\nrole_type: %s\nrig: gastown\nagent_state: %s"}`, id, id, labelJSON, id, role, state)
	}
	townList := "[" + strings.Join([]string{
		`{"id":"hq-mayor","title":"Mayor","issue_type":"task","status":"open","labels":["gt:agent"]}`,
		`{"id":"hq-deacon","title":"Deacon","issue_type":"task","status":"open","labels":["gt:agent"]}`,
		agent("gs-gastown-witness", "witness", "running"),
		agent("gs-gastown-refinery", "refinery", "working"),
		agent("gs-gastown-polecat-rust", "polecat", "working", "done-cp:pushed:polecat/rust/gs-1:1700000001", "safety_stop:gs-stop-1"),
		agent("gs-gastown-polecat-fury", "polecat", "working"),
	}, ",") + "]"
	rigList := "[" + agent("gs-gastown-polecat-fury", "polecat", "idle") + "]"

	logFile = filepath.Join(townRoot, "bd.log")
	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -uo pipefail
logfile=` + fmt.Sprintf("%q", logFile) + `
town=` + fmt.Sprintf("%q", townBeads) + `
townlist=` + fmt.Sprintf("%q", townList) + `
riglist=` + fmt.Sprintf("%q", rigList) + `
cmd=""; rest=()
for arg in "$@"; do
  if [[ -z "$cmd" && "$arg" != -* ]]; then cmd="$arg"; continue; fi
  [[ -n "$cmd" ]] && rest+=("$arg")
done
dir="${BEADS_DIR:-<unset>}"
case "$cmd" in
  list)
    if [[ "$dir" == "$town" ]]; then printf '%s\n' "$townlist"; else printf '%s\n' "$riglist"; fi
    ;;
  show)
    id="${rest[0]:-}"
    list="$riglist"
    if [[ "$dir" == "$town" ]]; then list="$townlist"; fi
    printf '%s\n' "$list" | python3 -c 'import json,sys; want=sys.argv[1]; m=[i for i in json.load(sys.stdin) if i["id"]==want]; print(json.dumps(m)) if m else sys.exit(1)' "$id" && exit 0
    echo "Error: no issue found" >&2; exit 1
    ;;
  create)
    id=""; desc=""; labels=""
    for arg in "${rest[@]}"; do
      case "$arg" in
        --id=*) id="${arg#--id=}" ;;
        --description=*) desc="${arg#--description=}" ;;
        --labels=*) labels="${arg#--labels=}" ;;
      esac
    done
    printf 'create %s dir=%s labels=%s desc=%s\n' "$id" "$dir" "$labels" "${desc//$'\n'/|}" >> "$logfile"
    printf '{"id":"%s","title":"t","status":"open","labels":["gt:agent"]}\n' "$id"
    ;;
  update|delete|close|reopen)
    printf '%s %s dir=%s\n' "$cmd" "${rest[0]:-}" "$dir" >> "$logfile"
    printf '{}\n'
    ;;
  *)
    exit 0
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return townRoot, rigBeads, logFile
}

func TestAgentBeadsExistCheck_FlagsTownOnlyRigAgents(t *testing.T) {
	townRoot, _, _ := townOnlyAgentTown(t)

	res := NewAgentBeadsCheck().Run(&CheckContext{TownRoot: townRoot})
	if res.Status != StatusWarning {
		t.Fatalf("Run() status = %v (%s), want warning for town-only rig agents; details=%v", res.Status, res.Message, res.Details)
	}
	joined := strings.Join(res.Details, "\n")
	for _, want := range []string{
		"gs-gastown-witness (town-only",
		"gs-gastown-refinery (town-only",
		"gs-gastown-polecat-rust (town-only",
		"gs-gastown-polecat-fury (rig-local copy is canonical; town duplicate is stale)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Run() details missing %q: %v", want, res.Details)
		}
	}
	if strings.Contains(joined, "hq-") {
		t.Errorf("Run() flagged town roles: %v", res.Details)
	}
}

// Reconciliation (gt doctor --fix, also run by gt upgrade after a daemon
// restart) migrates town-only rig agent beads, polecats included, into the
// rig DB with their state and labels, and retires (closes, never deletes or
// rewrites) the legacy town copies and stale town duplicates.
func TestAgentBeadsExistCheck_FixMigratesTownOnlyAgentsToRig(t *testing.T) {
	townRoot, rigBeads, logFile := townOnlyAgentTown(t)

	if err := NewAgentBeadsCheck().Fix(&CheckContext{TownRoot: townRoot}); err != nil {
		t.Fatalf("Fix() returned error: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading fake bd log: %v", err)
	}
	log := string(data)
	townBeads := filepath.Join(townRoot, ".beads")
	want := map[string][]string{
		"gs-gastown-witness":      {"agent_state: running"},
		"gs-gastown-refinery":     {"agent_state: working"},
		"gs-gastown-polecat-rust": {"agent_state: working", "labels=gt:agent,done-cp:pushed:polecat/rust/gs-1:1700000001,safety_stop:gs-stop-1 "},
	}
	for id, carried := range want {
		line := ""
		for _, l := range strings.Split(log, "\n") {
			if strings.HasPrefix(l, "create "+id+" ") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("Fix() did not migrate %s; log:\n%s", id, log)
		}
		if !strings.Contains(line, "dir="+rigBeads+" ") {
			t.Errorf("Fix() created %s outside the rig DB: %s", id, line)
		}
		for _, field := range carried {
			if !strings.Contains(line, field) {
				t.Errorf("Fix() did not carry %q for %s: %s", field, id, line)
			}
		}
		if !strings.Contains(log, "close "+id+" dir="+townBeads+"\n") {
			t.Errorf("Fix() did not retire the town copy of %s; log:\n%s", id, log)
		}
	}
	if !strings.Contains(log, "close gs-gastown-polecat-fury dir="+townBeads+"\n") {
		t.Errorf("Fix() did not retire the stale town duplicate of polecat fury; log:\n%s", log)
	}
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		if strings.Contains(l, "hq-") {
			t.Errorf("Fix() touched a town role bead: %s", l)
		}
		if strings.HasPrefix(l, "create gs-gastown-polecat-fury ") {
			t.Errorf("Fix() recreated the rig-local polecat fury: %s", l)
		}
		if !strings.HasPrefix(l, "create ") && !strings.HasPrefix(l, "close ") {
			t.Errorf("Fix() mutated an existing bead: %s", l)
		}
	}
}
