package polecat

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/testutil"
)

// initRigBeadsForAddTest initializes the rig beads database used by
// AddWithOptions fixtures laid out as <town>/<rigName>/mayor/rig.
//
// With real bd, every test gets its own random prefix (and therefore its own
// beads_<prefix> database on the shared Dolt test container). Reusing a fixed
// prefix makes the second `bd init` in the same package write a fresh project
// ID into metadata.json while the server still holds the first test's
// database, so later bd calls fail with PROJECT IDENTITY MISMATCH (gs-ct8).
// A town routes.jsonl entry maps the prefix to the rig so the manager derives
// agent bead IDs with the same prefix the database was initialized with.
//
// Without bd (e.g. Windows CI) a mock bd is installed instead.
func initRigBeadsForAddTest(t *testing.T, rigPath string) {
	t.Helper()

	mayorRig := filepath.Join(rigPath, "mayor", "rig")
	if _, err := exec.LookPath("bd"); err != nil {
		installMockBd(t)
		// Write the type-config sentinel so EnsureCustomTypes is a no-op.
		_ = os.WriteFile(filepath.Join(mayorRig, ".beads", ".gt-types-configured"), []byte(beads.TypeConfigSentinelValue()+"\n"), 0644)
		return
	}

	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())

	prefix := randomAddTestPrefix(t)
	if err := beads.NewIsolatedWithPort(mayorRig, port).Init(prefix); err != nil {
		t.Fatalf("bd init: %v", err)
	}
	t.Cleanup(func() { dropAddTestDatabase(t, "beads_"+prefix) })

	writeTownRoute(t, filepath.Dir(rigPath), prefix, filepath.Base(rigPath))
}

func randomAddTestPrefix(t *testing.T) string {
	t.Helper()
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "pc" + hex.EncodeToString(buf[:])
}

func writeTownRoute(t *testing.T, townRoot, prefix, rigName string) {
	t.Helper()
	townBeads := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(townBeads, 0755); err != nil {
		t.Fatalf("mkdir town .beads: %v", err)
	}
	route, err := json.Marshal(beads.Route{Prefix: prefix + "-", Path: rigName + "/mayor/rig"})
	if err != nil {
		t.Fatalf("marshal route: %v", err)
	}
	if err := os.WriteFile(filepath.Join(townBeads, "routes.jsonl"), append(route, '\n'), 0644); err != nil {
		t.Fatalf("write routes.jsonl: %v", err)
	}
}

// dropAddTestDatabase keeps the shared Dolt test container free of per-test
// databases once the test finishes.
func dropAddTestDatabase(t *testing.T, dbName string) {
	t.Helper()
	db, err := sql.Open("mysql", "root:@tcp("+testutil.DoltContainerAddr()+")/")
	if err != nil {
		t.Logf("cleanup: connect to drop %s: %v", dbName, err)
		return
	}
	defer db.Close()
	if _, err := db.Exec("DROP DATABASE IF EXISTS `" + dbName + "`"); err != nil {
		t.Logf("cleanup: drop %s: %v", dbName, err)
	}
}

// sourceRepoSnapshot captures the source repository state that polecat
// creation must not change: HEAD plus the full status, including ignored
// paths, so files written under gitignored dirs like .claude/ are caught too.
//
// The rig's canonical beads store (.beads/ and bd's sibling .beads.gate.lock)
// is excluded: worktree redirects point there by design, and bd plus rig-level
// provisioning (PRIME.md, type sentinel) legitimately write to it.
func sourceRepoSnapshot(t *testing.T, repo string) string {
	t.Helper()
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD in %s: %v\n%s", repo, err, head)
	}
	status, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--ignored", "--",
		".", ":(exclude).beads", ":(exclude).beads.gate.lock").CombinedOutput()
	if err != nil {
		t.Fatalf("git status in %s: %v\n%s", repo, err, status)
	}
	return "HEAD " + string(head) + string(status)
}

// assertNoManagedSettingsIn fails if Claude settings were installed in dir.
func assertNoManagedSettingsIn(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, ".claude", "settings.json")
	if _, err := os.Stat(path); err == nil {
		t.Errorf("managed settings must not be installed at %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
