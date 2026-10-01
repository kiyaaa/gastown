package beads

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalActorIdentity(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"mayor", "mayor/"},
		{"mayor/", "mayor/"},
		{"deacon", "deacon/"},
		{"deacon/", "deacon/"},
		// Rig-scoped identities keep every meaningful path component.
		{"gastown/witness", "gastown/witness"},
		{"gastown/refinery", "gastown/refinery"},
		{"gastown/polecats/Toast", "gastown/polecats/Toast"},
		{"gastown/crew/max", "gastown/crew/max"},
		{"gastown/Toast", "gastown/Toast"},
		{"gastown/mayor", "gastown/mayor"},
		{"deacon-boot", "deacon-boot"},
		{"deacon/dogs/alpha", "deacon/dogs/alpha"},
		{"overseer", "overseer"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := CanonicalActorIdentity(tt.in); got != tt.want {
			t.Errorf("CanonicalActorIdentity(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOwnerActorForAssignee(t *testing.T) {
	tests := []struct {
		name, assignee, actor, want string
	}{
		{"mayor session owns slash-assigned record", "mayor/", "mayor", "mayor/"},
		{"deacon session owns slash-assigned record", "deacon/", "deacon", "deacon/"},
		{"legacy bare record with slash actor", "mayor", "mayor/", "mayor"},
		{"exact match unchanged", "mayor/", "mayor/", "mayor/"},
		{"different role is not reconciled", "mayor/", "deacon", "deacon"},
		{"deacon cannot take mayor record", "deacon/", "mayor", "mayor"},
		{"rig witness is not the town mayor", "mayor/", "gastown/mayor", "gastown/mayor"},
		{"witnesses in different rigs stay distinct", "gastown/witness", "beads/witness", "beads/witness"},
		{"polecat path components preserved", "gastown/Toast", "gastown/polecats/Toast", "gastown/polecats/Toast"},
		{"crew path components preserved", "gastown/crew/max", "gastown/crew/joe", "gastown/crew/joe"},
		{"unassigned record unchanged", "", "mayor", "mayor"},
		{"empty actor never acquires ownership", "mayor/", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OwnerActorForAssignee(tt.assignee, tt.actor); got != tt.want {
				t.Errorf("OwnerActorForAssignee(%q, %q) = %q, want %q", tt.assignee, tt.actor, got, tt.want)
			}
		})
	}
}

func TestProcessActorPrefersBeadsActor(t *testing.T) {
	t.Setenv("BEADS_ACTOR", "")
	t.Setenv("BD_ACTOR", "mayor")
	if got := ProcessActor(); got != "mayor" {
		t.Fatalf("ProcessActor() = %q, want BD_ACTOR fallback %q", got, "mayor")
	}
	t.Setenv("BEADS_ACTOR", "deacon")
	if got := ProcessActor(); got != "deacon" {
		t.Fatalf("ProcessActor() = %q, want BEADS_ACTOR %q", got, "deacon")
	}
}

// installOwnershipBdStub installs a fake bd that serves one escalation record
// assigned to assignee and enforces bd 1.3.0's close ownership check: close is
// rejected unless the acting identity (--actor, then BEADS_ACTOR, then
// BD_ACTOR) equals the assignee byte-for-byte, as bd does for "mayor/" vs
// "mayor". Returns the path of a log holding each close's acting identity.
func installOwnershipBdStub(t *testing.T, assignee string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell bd stub is POSIX-only")
	}
	stubDir := t.TempDir()
	closeLog := filepath.Join(stubDir, "close.log")
	script := `#!/bin/sh
sub=""
actor_flag=""
for a in "$@"; do
  case "$a" in
    --actor=*) actor_flag="${a#--actor=}" ;;
    -*) ;;
    *) if [ -z "$sub" ]; then sub="$a"; fi ;;
  esac
done
case "$sub" in
  show)
    echo '[{"id":"hq-esc1","title":"[HIGH] test","status":"open","priority":1,"assignee":"` + assignee + `","labels":["gt:escalation"],"description":"severity: high"}]'
    ;;
  update) ;;
  close)
    actor="$actor_flag"
    [ -z "$actor" ] && actor="$BEADS_ACTOR"
    [ -z "$actor" ] && actor="$BD_ACTOR"
    printf '%s\n' "$actor" >> "` + closeLog + `"
    if [ "$actor" != "` + assignee + `" ]; then
      echo "Error: cannot close hq-esc1: assignee is \"` + assignee + `\", actor is \"$actor\"; reclaim or use --force to override" >&2
      exit 1
    fi
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(stubDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BEADS_ACTOR", "")
	ResetBdAllowStaleCacheForTest()
	t.Cleanup(ResetBdAllowStaleCacheForTest)
	return closeLog
}

func TestCloseEscalation_RoleOwnerSpellings(t *testing.T) {
	tests := []struct {
		name, assignee, bdActor string
	}{
		{"mayor session closes mayor/ record", "mayor/", "mayor"},
		{"deacon session closes deacon/ record", "deacon/", "deacon"},
		{"slash actor closes legacy bare record", "mayor", "mayor/"},
		{"exact spelling still closes", "mayor/", "mayor/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closeLog := installOwnershipBdStub(t, tt.assignee)
			t.Setenv("BD_ACTOR", tt.bdActor)

			b := New(t.TempDir())
			if err := b.CloseEscalation("hq-esc1", tt.bdActor, "resolved"); err != nil {
				t.Fatalf("CloseEscalation as %q on record assigned %q: %v", tt.bdActor, tt.assignee, err)
			}
			data, err := os.ReadFile(closeLog)
			if err != nil {
				t.Fatalf("read close log: %v", err)
			}
			if got := strings.TrimSpace(string(data)); got != tt.assignee {
				t.Errorf("close acted as %q, want record owner %q", got, tt.assignee)
			}
		})
	}
}

func TestCloseEscalation_RejectsDifferentActor(t *testing.T) {
	tests := []struct {
		name, assignee, bdActor string
	}{
		{"deacon cannot close mayor escalation", "mayor/", "deacon"},
		{"rig-scoped mayor spelling is not the town mayor", "mayor/", "gastown/mayor"},
		{"other rig witness cannot close", "gastown/witness", "beads/witness"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closeLog := installOwnershipBdStub(t, tt.assignee)
			t.Setenv("BD_ACTOR", tt.bdActor)

			b := New(t.TempDir())
			err := b.CloseEscalation("hq-esc1", tt.bdActor, "resolved")
			if err == nil {
				t.Fatalf("CloseEscalation as %q on record assigned %q succeeded, want ownership rejection", tt.bdActor, tt.assignee)
			}
			if !strings.Contains(err.Error(), "cannot close") {
				t.Errorf("unexpected error: %v", err)
			}
			data, _ := os.ReadFile(closeLog)
			if got := strings.TrimSpace(string(data)); got != tt.bdActor {
				t.Errorf("close acted as %q, want unchanged caller %q", got, tt.bdActor)
			}
		})
	}
}

func TestIsRoleOnlyActorIdentity(t *testing.T) {
	for _, in := range []string{"mayor", "mayor/", "deacon", "deacon/"} {
		if !IsRoleOnlyActorIdentity(in) {
			t.Errorf("IsRoleOnlyActorIdentity(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"", "mayor//", "gastown/mayor", "deacon-boot", "deacon/dogs/alpha", "gastown/witness", "overseer"} {
		if IsRoleOnlyActorIdentity(in) {
			t.Errorf("IsRoleOnlyActorIdentity(%q) = true, want false", in)
		}
	}
}
