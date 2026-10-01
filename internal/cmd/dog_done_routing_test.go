package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/gastown/internal/dog"
)

// =============================================================================
// gs-qed: `gt dog done` must not route through the top-level `gt done` gate
// =============================================================================

func TestIsDoneCommand_OnlyTopLevelDone(t *testing.T) {
	root := &cobra.Command{Use: "gt"}
	topDone := &cobra.Command{Use: "done"}
	dogCmd := &cobra.Command{Use: "dog"}
	dogDone := &cobra.Command{Use: "done"}
	root.AddCommand(topDone, dogCmd)
	dogCmd.AddCommand(dogDone)

	if !isDoneCommand(topDone) {
		t.Error("top-level done should be detected")
	}
	if isDoneCommand(dogDone) {
		t.Error("nested dog done must not be detected as top-level done")
	}
	if isDoneCommand(dogCmd) || isDoneCommand(root) {
		t.Error("non-done commands must not be detected as done")
	}
	if !isDoneCommand(&cobra.Command{Use: "done"}) {
		t.Error("detached done command should stay gated (fail closed)")
	}
}

func TestIsDoneInvocation_RealCommandTree(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"done"}, true},
		{[]string{"done", "--status", "DEFERRED"}, true},
		{[]string{"dog", "done"}, false},
		{[]string{"dog", "done", "alpha"}, false},
		{[]string{"mol", "step", "done", "gs-x"}, false},
		{[]string{"wl", "done", "w-1"}, false},
		{[]string{"dog", "clear", "alpha"}, false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			if got := isDoneInvocation(tt.args); got != tt.want {
				t.Errorf("isDoneInvocation(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDogDoneCommandWiredToDogHandler(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"dog", "done"})
	if err != nil {
		t.Fatalf("Find(dog done): %v", err)
	}
	if cmd != dogDoneCmd {
		t.Fatalf("dog done resolved to %q, want dogDoneCmd", cmd.CommandPath())
	}
	if cmd == doneCmd {
		t.Fatal("dog done resolved to top-level doneCmd")
	}
}

// Top-level `gt done` must keep enforcing polecat-only ownership.
func TestPersistentPreRunTopLevelDoneStillRejectsDogActor(t *testing.T) {
	t.Setenv("BD_ACTOR", "dog")
	t.Setenv("GT_ROLE", "dog")
	t.Setenv("GT_SESSION", "")

	err := persistentPreRun(doneCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "polecats only") {
		t.Fatalf("persistentPreRun(gt done) error = %v, want polecat-only rejection", err)
	}
}

// =============================================================================
// runDogDone state/hook transitions
// =============================================================================

// setupDogDoneTown creates an isolated town with a single dog and stubs out
// the mail and tmux side effects. Returns the town root and the recorded
// session-termination calls.
func setupDogDoneTown(t *testing.T, name string, state *dog.DogState) (string, *[]string) {
	t.Helper()
	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "rigs.json"), []byte(`{"version":1,"rigs":{}}`), 0644); err != nil {
		t.Fatalf("write rigs.json: %v", err)
	}
	t.Setenv("GT_TOWN_ROOT", townRoot)
	t.Setenv("GT_ROOT", townRoot)

	if state != nil {
		m, err := getDogManagerAt(townRoot)
		if err != nil {
			t.Fatal(err)
		}
		setupTestDog(t, m, townRoot, name, state)
	}

	origMails, origTerminate := dogDoneCloseMails, dogDoneTerminateSession
	var terminated []string
	dogDoneCloseMails = func(string) {}
	dogDoneTerminateSession = func(n string) { terminated = append(terminated, n) }
	t.Cleanup(func() {
		dogDoneCloseMails, dogDoneTerminateSession = origMails, origTerminate
	})
	return townRoot, &terminated
}

func getDogManagerAt(townRoot string) (*dog.Manager, error) {
	orig, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(townRoot); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chdir(orig) }()
	return getDogManager()
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func workingDogState(name string) *dog.DogState {
	now := time.Now()
	return &dog.DogState{
		Name:          name,
		State:         dog.StateWorking,
		Work:          "hq-wisp-2afe",
		WorkStartedAt: now,
		LastActive:    now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func readDog(t *testing.T, townRoot, name string) *dog.Dog {
	t.Helper()
	m, err := getDogManagerAt(townRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.Get(name)
	if err != nil {
		t.Fatalf("Get(%s): %v", name, err)
	}
	return d
}

func TestRunDogDone_AutoDetectFromWorktreeClearsHookWithoutPolecatActor(t *testing.T) {
	townRoot, terminated := setupDogDoneTown(t, "alpha", workingDogState("alpha"))
	// Mirror the failing repro: dog actor, not a polecat.
	t.Setenv("BD_ACTOR", "dog")
	chdirForTest(t, filepath.Join(townRoot, "deacon", "dogs", "alpha", "gastown"))

	if err := runDogDone(dogDoneCmd, nil); err != nil {
		t.Fatalf("runDogDone: %v", err)
	}

	d := readDog(t, townRoot, "alpha")
	if d.State != dog.StateIdle {
		t.Errorf("State = %q, want idle", d.State)
	}
	if d.Work != "" {
		t.Errorf("Work = %q, want cleared", d.Work)
	}
	if len(*terminated) != 1 || (*terminated)[0] != "alpha" {
		t.Errorf("terminated sessions = %v, want [alpha]", *terminated)
	}
}

func TestRunDogDone_ExplicitNameWithBDActorUnset(t *testing.T) {
	townRoot, terminated := setupDogDoneTown(t, "bravo", workingDogState("bravo"))
	t.Setenv("BD_ACTOR", "")
	chdirForTest(t, townRoot)

	if err := runDogDone(dogDoneCmd, []string{"bravo"}); err != nil {
		t.Fatalf("runDogDone: %v", err)
	}
	if d := readDog(t, townRoot, "bravo"); d.State != dog.StateIdle || d.Work != "" {
		t.Errorf("dog = {State:%q Work:%q}, want idle with no work", d.State, d.Work)
	}
	if len(*terminated) != 1 {
		t.Errorf("terminated sessions = %v, want one", *terminated)
	}
}

func TestRunDogDone_AlreadyIdleDoesNotTerminate(t *testing.T) {
	now := time.Now()
	townRoot, terminated := setupDogDoneTown(t, "alpha", &dog.DogState{
		Name: "alpha", State: dog.StateIdle, LastActive: now, CreatedAt: now, UpdatedAt: now,
	})
	chdirForTest(t, townRoot)

	if err := runDogDone(dogDoneCmd, []string{"alpha"}); err != nil {
		t.Fatalf("runDogDone: %v", err)
	}
	if len(*terminated) != 0 {
		t.Errorf("terminated sessions = %v, want none for already-idle dog", *terminated)
	}
}

func TestRunDogDone_MissingIdentity(t *testing.T) {
	townRoot, terminated := setupDogDoneTown(t, "alpha", workingDogState("alpha"))
	// Not inside deacon/dogs/<name>/ and no name argument.
	chdirForTest(t, filepath.Join(townRoot, "gastown", "polecats", "fury"))

	err := runDogDone(dogDoneCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "could not detect dog name") {
		t.Fatalf("runDogDone error = %v, want dog-name detection failure", err)
	}
	if strings.Contains(err.Error(), "polecats only") || strings.Contains(err.Error(), "BD_ACTOR") {
		t.Errorf("error %q leaks polecat gate wording", err)
	}
	if d := readDog(t, townRoot, "alpha"); d.State != dog.StateWorking || d.Work != "hq-wisp-2afe" {
		t.Errorf("unrelated dog mutated: {State:%q Work:%q}", d.State, d.Work)
	}
	if len(*terminated) != 0 {
		t.Errorf("terminated sessions = %v, want none", *terminated)
	}
}

func TestRunDogDone_InvalidIdentity(t *testing.T) {
	townRoot, terminated := setupDogDoneTown(t, "alpha", workingDogState("alpha"))
	chdirForTest(t, townRoot)

	tests := []struct {
		name    string
		arg     string
		wantErr error
	}{
		{"unknown dog", "ghost", dog.ErrDogNotFound},
		{"path traversal", "../alpha", dog.ErrInvalidName},
		{"path separator", "alpha/x", dog.ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runDogDone(dogDoneCmd, []string{tt.arg})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("runDogDone(%q) error = %v, want %v", tt.arg, err, tt.wantErr)
			}
		})
	}
	if d := readDog(t, townRoot, "alpha"); d.State != dog.StateWorking || d.Work != "hq-wisp-2afe" {
		t.Errorf("dog mutated by invalid invocations: {State:%q Work:%q}", d.State, d.Work)
	}
	if len(*terminated) != 0 {
		t.Errorf("terminated sessions = %v, want none", *terminated)
	}
}
