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
	inv.townIssues["hq-mayor"] = &beads.Issue{ID: "hq-mayor"}
	inv.townIssues["gs-gastown-refinery"] = &beads.Issue{ID: "gs-gastown-refinery"}
	inv.rigIssues["gs-gastown-refinery"] = &beads.Issue{ID: "gs-gastown-refinery", Labels: []string{"gt:agent"}}

	tests := []struct {
		id        string
		want      agentBeadPresence
		wantIssue bool
	}{
		{"gs-gastown-witness", agentBeadTownOnly, true},     // rig-scoped, town copy only
		{"gs-gastown-crew-max", agentBeadTownOnly, false},   // rig-scoped, town wisp only
		{"gs-gastown-refinery", agentBeadPresent, true},     // duplicate: rig copy wins
		{"gs-gastown-polecat-rust", agentBeadPresent, true}, // polecats are town-owned
		{"hq-mayor", agentBeadPresent, true},                // town role stays in hq
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
}

func TestAgentBeadsCheckResult_MisplacedIsFixableWarning(t *testing.T) {
	c := NewAgentBeadsCheck()
	res := c.result(4, nil, []string{"gs-gastown-witness"}, nil)
	if res.Status != StatusWarning {
		t.Fatalf("status = %v, want warning", res.Status)
	}
	if !strings.Contains(res.Message, "only in town beads") || len(res.Details) != 1 || !strings.Contains(res.Details[0], "gs-gastown-witness") {
		t.Fatalf("result = %+v, want misplaced witness diagnostic", res)
	}

	if res := c.result(4, []string{"gs-gastown-refinery"}, []string{"gs-gastown-witness"}, nil); res.Status != StatusError {
		t.Fatalf("missing bead status = %v, want error", res.Status)
	}
	if res := c.result(4, nil, nil, nil); res.Status != StatusOK {
		t.Fatalf("clean status = %v, want OK", res.Status)
	}
}

// townOnlyAgentTown builds a town whose witness and refinery beads exist only
// in the town database (the hq-ou3 state after daemon restart), plus a
// polecat bead that legitimately lives in town.
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

	agent := func(id, role, state string) string {
		return fmt.Sprintf(`{"id":"%s","title":"%s title","issue_type":"task","status":"open","labels":["gt:agent"],"description":"%s title\n\nrole_type: %s\nrig: gastown\nagent_state: %s"}`, id, id, id, role, state)
	}
	townList := "[" + strings.Join([]string{
		`{"id":"hq-mayor","title":"Mayor","issue_type":"task","status":"open","labels":["gt:agent"]}`,
		`{"id":"hq-deacon","title":"Deacon","issue_type":"task","status":"open","labels":["gt:agent"]}`,
		agent("gs-gastown-witness", "witness", "running"),
		agent("gs-gastown-refinery", "refinery", "working"),
		agent("gs-gastown-polecat-rust", "polecat", "working"),
	}, ",") + "]"

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
cmd=""; rest=()
for arg in "$@"; do
  if [[ -z "$cmd" && "$arg" != -* ]]; then cmd="$arg"; continue; fi
  [[ -n "$cmd" ]] && rest+=("$arg")
done
dir="${BEADS_DIR:-<unset>}"
case "$cmd" in
  list)
    if [[ "$dir" == "$town" ]]; then printf '%s\n' "$townlist"; else printf '[]\n'; fi
    ;;
  show)
    id="${rest[0]:-}"
    if [[ "$dir" == "$town" ]]; then
      printf '%s\n' "$townlist" | python3 -c 'import json,sys; want=sys.argv[1]; m=[i for i in json.load(sys.stdin) if i["id"]==want]; print(json.dumps(m)) if m else sys.exit(1)' "$id" && exit 0
    fi
    echo "Error: no issue found" >&2; exit 1
    ;;
  create)
    id=""; desc=""
    for arg in "${rest[@]}"; do
      case "$arg" in
        --id=*) id="${arg#--id=}" ;;
        --description=*) desc="${arg#--description=}" ;;
      esac
    done
    printf 'create %s dir=%s desc=%s\n' "$id" "$dir" "${desc//$'\n'/|}" >> "$logfile"
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
	for _, id := range []string{"gs-gastown-witness", "gs-gastown-refinery"} {
		if !strings.Contains(joined, id) {
			t.Errorf("Run() details missing misplaced %s: %v", id, res.Details)
		}
	}
	if strings.Contains(joined, "polecat") || strings.Contains(joined, "hq-") {
		t.Errorf("Run() flagged town-owned beads as misplaced: %v", res.Details)
	}
}

// Reconciliation (gt doctor --fix, also run by gt upgrade after a daemon
// restart) creates rig-local copies carrying the town state, and never
// mutates or deletes the legacy town copies.
func TestAgentBeadsExistCheck_FixCreatesRigLocalCopyFromTown(t *testing.T) {
	townRoot, rigBeads, logFile := townOnlyAgentTown(t)

	if err := NewAgentBeadsCheck().Fix(&CheckContext{TownRoot: townRoot}); err != nil {
		t.Fatalf("Fix() returned error: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading fake bd log: %v", err)
	}
	log := string(data)
	want := map[string]string{
		"gs-gastown-witness":  "agent_state: running",
		"gs-gastown-refinery": "agent_state: working",
	}
	for id, state := range want {
		line := ""
		for _, l := range strings.Split(log, "\n") {
			if strings.HasPrefix(l, "create "+id+" ") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("Fix() did not create rig-local %s; log:\n%s", id, log)
		}
		if !strings.Contains(line, "dir="+rigBeads+" ") {
			t.Errorf("Fix() created %s outside the rig DB: %s", id, line)
		}
		if !strings.Contains(line, state) {
			t.Errorf("Fix() did not carry town state %q for %s: %s", state, id, line)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		if strings.Contains(l, "polecat") || strings.Contains(l, "hq-") {
			t.Errorf("Fix() touched a town-owned bead: %s", l)
		}
		if !strings.HasPrefix(l, "create ") {
			t.Errorf("Fix() mutated an existing bead: %s", l)
		}
	}
}
