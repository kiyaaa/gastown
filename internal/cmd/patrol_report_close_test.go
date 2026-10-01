package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// patrolStepWispStub models a poured patrol molecule (gs-rxy / hq-sud): the
// root and every step live in the wisps table, steps carry the formula's
// priority (P2, not P0), and one step has a nested sub-step. The durable
// issues table holds no children, so issue-only `bd list --parent` (or any
// priority=0 filter) finds nothing and the steps leak when the root is closed.
const patrolStepWispStub = `#!/bin/sh
while [ "$1" = "--allow-stale" ]; do shift; done
cmd="$1"
shift || true
case "$cmd" in
  list)
    echo '[]'
    ;;
  query)
    if [ -n "%[2]s" ]; then
      echo 'Error: database locked' >&2
      exit 1
    fi
    case "$*" in
      *priority=*)
        echo '[]'
        ;;
      *'parent="hq-wisp-root"'*)
        echo '[{"id":"hq-wisp-s1","title":"heartbeat","status":"open","priority":2,"ephemeral":true},{"id":"hq-wisp-s2","title":"inbox-check","status":"in_progress","priority":2,"ephemeral":true},{"id":"hq-wisp-s3","title":"health-scan","status":"closed","priority":2,"ephemeral":true}]'
        ;;
      *'parent="hq-wisp-s2"'*)
        echo '[{"id":"hq-wisp-s2a","title":"inbox-check.sub","status":"open","priority":2,"ephemeral":true}]'
        ;;
      *)
        echo '[]'
        ;;
    esac
    ;;
  close)
    for arg in "$@"; do
      case "$arg" in --*) continue ;; esac
      echo "$arg" >> "%[1]s"
    done
    ;;
esac
exit 0
`

// setupPatrolStepWispStub installs the stub bd on PATH and returns an
// isolated Beads instance plus the path of the close log.
func setupPatrolStepWispStub(t *testing.T, failQuery bool) (*beads.Beads, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script bd stub not supported on Windows")
	}

	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, ".beads"), 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	closesLog := filepath.Join(townRoot, "closes.log")
	fail := ""
	if failQuery {
		fail = "1"
	}
	script := fmt.Sprintf(patrolStepWispStub, closesLog, fail)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BEADS_DIR", "")
	return beads.NewIsolated(townRoot), closesLog
}

func readCloseLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read close log: %v", err)
	}
	return strings.Fields(string(data))
}

// TestForceCloseDescendants_ClosesEphemeralStepWisps reproduces the gs-rxy
// leak: descendants that exist only in the wisps table, at non-zero
// priority, must still be found and closed.
func TestForceCloseDescendants_ClosesEphemeralStepWisps(t *testing.T) {
	b, closesLog := setupPatrolStepWispStub(t, false)

	closed, err := forceCloseDescendants(b, "hq-wisp-root")
	if err != nil {
		t.Fatalf("forceCloseDescendants: %v", err)
	}

	got := readCloseLog(t, closesLog)
	for _, id := range []string{"hq-wisp-s1", "hq-wisp-s2", "hq-wisp-s2a"} {
		if slices.Index(got, id) < 0 {
			t.Errorf("step wisp %s was not closed (leaked); closes=%v", id, got)
		}
	}
	if slices.Index(got, "hq-wisp-s3") >= 0 {
		t.Errorf("already-closed step hq-wisp-s3 should not be re-closed; closes=%v", got)
	}
	if slices.Index(got, "hq-wisp-s2a") > slices.Index(got, "hq-wisp-s2") {
		t.Errorf("nested step should close before its parent; closes=%v", got)
	}
	if closed != 3 {
		t.Errorf("closed = %d, want 3", closed)
	}
}

// TestClosePatrolCycle_ClosesStepsBeforeRoot verifies gt patrol report closes
// every open poured step before closing (and thereby releasing for GC) the
// patrol root.
func TestClosePatrolCycle_ClosesStepsBeforeRoot(t *testing.T) {
	b, closesLog := setupPatrolStepWispStub(t, false)

	if err := closePatrolCycle(b, "hq-wisp-root", "All clear"); err != nil {
		t.Fatalf("closePatrolCycle: %v", err)
	}

	got := readCloseLog(t, closesLog)
	rootIdx := slices.Index(got, "hq-wisp-root")
	if rootIdx < 0 {
		t.Fatalf("patrol root was not closed; closes=%v", got)
	}
	for _, id := range []string{"hq-wisp-s1", "hq-wisp-s2", "hq-wisp-s2a"} {
		idx := slices.Index(got, id)
		if idx < 0 {
			t.Errorf("step wisp %s leaked: not closed before root; closes=%v", id, got)
			continue
		}
		if idx > rootIdx {
			t.Errorf("step wisp %s closed after root; closes=%v", id, got)
		}
	}
}

// TestClosePatrolCycle_KeepsRootWhenStepListingFails preserves gt-7lx3: if
// descendants cannot be enumerated, the root must stay open so the next
// cycle retries instead of orphaning the steps.
func TestClosePatrolCycle_KeepsRootWhenStepListingFails(t *testing.T) {
	b, closesLog := setupPatrolStepWispStub(t, true)

	if err := closePatrolCycle(b, "hq-wisp-root", "All clear"); err == nil {
		t.Fatal("closePatrolCycle succeeded despite step listing failure")
	}

	if got := readCloseLog(t, closesLog); slices.Index(got, "hq-wisp-root") >= 0 {
		t.Errorf("patrol root closed even though steps could not be listed; closes=%v", got)
	}
}
