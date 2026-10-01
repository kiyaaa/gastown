package mail

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// installMailOwnershipBd installs a fake bd serving one message assigned to
// assignee and enforcing bd 1.3.0's close ownership check: close fails unless
// the acting identity (--actor, then BEADS_ACTOR, then BD_ACTOR) equals the
// assignee byte-for-byte, as bd does for "mayor/" vs "mayor" (hq-4vo).
// Returns the beads dir and a log of each close's acting identity.
func installMailOwnershipBd(t *testing.T, assignee string) (beadsDir, closeLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake bd is POSIX-only")
	}
	beadsDir = t.TempDir()
	binDir := t.TempDir()
	closeLog = filepath.Join(t.TempDir(), "close.log")
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
    printf '%s\n' '[{"id":"hq-msg1","title":"Hello","description":"body","status":"open","priority":2,"assignee":"` + assignee + `","created_at":"2026-10-01T12:00:00Z","labels":["gt:message","from:gastown/witness"]}]'
    ;;
  close)
    actor="$actor_flag"
    [ -z "$actor" ] && actor="$BEADS_ACTOR"
    [ -z "$actor" ] && actor="$BD_ACTOR"
    printf '%s\n' "$actor" >> "` + closeLog + `"
    if [ "$actor" != "` + assignee + `" ]; then
      echo "Error: cannot close hq-msg1: assignee is \"` + assignee + `\", actor is \"$actor\"; reclaim or use --force to override" >&2
      exit 1
    fi
    ;;
  *)
    printf 'unexpected bd args: %s\n' "$*" >&2
    exit 1
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BEADS_ACTOR", "")
	return beadsDir, closeLog
}

func readCloseActors(t *testing.T, closeLog string) []string {
	t.Helper()
	data, err := os.ReadFile(closeLog)
	if err != nil {
		t.Fatalf("read close log: %v", err)
	}
	return strings.Fields(string(data))
}

func TestMailboxArchiveDelete_RoleOwnerSpellings(t *testing.T) {
	tests := []struct {
		name, mailbox, assignee, bdActor string
	}{
		{"mayor session, mayor/ record", "mayor/", "mayor/", "mayor"},
		{"deacon session, deacon/ record", "deacon/", "deacon/", "deacon"},
		{"mayor session, legacy bare record", "mayor/", "mayor", "mayor"},
		{"slash actor, legacy bare record", "mayor", "mayor", "mayor/"},
		{"rig witness exact match", "gastown/witness", "gastown/witness", "gastown/witness"},
	}
	ops := map[string]func(*Mailbox, string) error{
		"archive": (*Mailbox).Archive,
		"delete":  (*Mailbox).Delete,
	}
	for _, tt := range tests {
		for opName, op := range ops {
			t.Run(tt.name+"/"+opName, func(t *testing.T) {
				beadsDir, closeLog := installMailOwnershipBd(t, tt.assignee)
				t.Setenv("BD_ACTOR", tt.bdActor)

				m := NewMailboxWithBeadsDir(tt.mailbox, t.TempDir(), beadsDir)
				if err := op(m, "hq-msg1"); err != nil {
					t.Fatalf("%s as %q on message assigned %q: %v", opName, tt.bdActor, tt.assignee, err)
				}
				actors := readCloseActors(t, closeLog)
				if len(actors) != 1 || actors[0] != tt.assignee {
					t.Errorf("close acted as %q, want single close as record owner %q", actors, tt.assignee)
				}
			})
		}
	}
}

func TestMailboxDelete_RejectsDifferentActor(t *testing.T) {
	tests := []struct {
		name, mailbox, assignee, bdActor string
	}{
		{"deacon cannot close mayor mail", "mayor/", "mayor/", "deacon"},
		{"mayor cannot close deacon mail", "deacon/", "deacon/", "mayor"},
		{"rig-scoped mayor spelling is not the town mayor", "mayor/", "mayor/", "gastown/mayor"},
		{"other rig witness cannot close", "gastown/witness", "gastown/witness", "beads/witness"},
		{"other crew member cannot close", "gastown/crew/max", "gastown/crew/max", "gastown/crew/joe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir, closeLog := installMailOwnershipBd(t, tt.assignee)
			t.Setenv("BD_ACTOR", tt.bdActor)

			m := NewMailboxWithBeadsDir(tt.mailbox, t.TempDir(), beadsDir)
			err := m.Delete("hq-msg1")
			if err == nil {
				t.Fatalf("delete as %q on message assigned %q succeeded, want ownership rejection", tt.bdActor, tt.assignee)
			}
			if !strings.Contains(err.Error(), "cannot close") {
				t.Errorf("unexpected error: %v", err)
			}
			actors := readCloseActors(t, closeLog)
			if len(actors) != 1 || actors[0] != tt.bdActor {
				t.Errorf("close acted as %q, want unchanged caller %q", actors, tt.bdActor)
			}
		})
	}
}

// TestRoleIdentityCanonicalFormMatchesMailAddress pins the single canonical
// rule: the identity mail assigns to a role-only recipient is exactly the
// canonical actor identity used at the bd ownership boundary.
func TestRoleIdentityCanonicalFormMatchesMailAddress(t *testing.T) {
	for _, in := range []string{"mayor", "mayor/", "deacon", "deacon/"} {
		if got, want := AddressToIdentity(in), beads.CanonicalActorIdentity(in); got != want {
			t.Errorf("AddressToIdentity(%q) = %q, canonical actor identity = %q", in, got, want)
		}
	}
}
