package mail

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// makeWorkerTown builds a town with polecat Toast, crew max, and the name dup
// held by both a polecat and a crew member in rig gastown.
func makeWorkerTown(t *testing.T) string {
	t.Helper()
	townRoot := t.TempDir()
	for _, dir := range []string{
		"mayor",
		".beads",
		"gastown/witness",
		"gastown/refinery",
		"gastown/polecats/Toast",
		"gastown/crew/max",
		"gastown/polecats/dup",
		"gastown/crew/dup",
	} {
		if err := os.MkdirAll(filepath.Join(townRoot, dir), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return townRoot
}

func TestMailAssigneeIdentity(t *testing.T) {
	r := &Router{townRoot: makeWorkerTown(t)}
	tests := []struct {
		address, want string
	}{
		// Explicit worker addresses keep their qualifier.
		{"gastown/polecats/Toast", "gastown/polecats/Toast"},
		{"gastown/polecat/Toast", "gastown/polecats/Toast"},
		{"gastown/crew/max", "gastown/crew/max"},
		{"gastown/polecats/dup", "gastown/polecats/dup"},
		{"gastown/crew/dup", "gastown/crew/dup"},
		// Bare rig/name is qualified only when exactly one worker holds it.
		{"gastown/Toast", "gastown/polecats/Toast"},
		{"gastown/max", "gastown/crew/max"},
		{"gastown/dup", "gastown/dup"},
		{"gastown/ghost", "gastown/ghost"},
		// Roles are never workers.
		{"gastown/witness", "gastown/witness"},
		{"gastown/refinery", "gastown/refinery"},
		{"mayor/", "mayor/"},
		{"deacon", "deacon/"},
		{"deacon/dogs/alpha", "deacon/dogs/alpha"},
		{"overseer", "overseer"},
	}
	for _, tt := range tests {
		if got := r.MailAssigneeIdentity(tt.address); got != tt.want {
			t.Errorf("MailAssigneeIdentity(%q) = %q, want %q", tt.address, got, tt.want)
		}
	}

	// Without a town root a bare address cannot be resolved.
	if got := (&Router{}).MailAssigneeIdentity("gastown/Toast"); got != "gastown/Toast" {
		t.Errorf("MailAssigneeIdentity without town = %q, want bare %q", got, "gastown/Toast")
	}
}

func TestMailboxAssigneeVariantsIncludeWorkerOwner(t *testing.T) {
	r := &Router{townRoot: makeWorkerTown(t), workDir: t.TempDir()}
	tests := []struct {
		address string
		want    []string
	}{
		{"gastown/polecats/Toast", []string{"gastown/Toast", "gastown/polecats/Toast"}},
		{"gastown/Toast", []string{"gastown/Toast", "gastown/polecats/Toast"}},
		{"gastown/crew/dup", []string{"gastown/dup", "gastown/crew/dup"}},
		{"gastown/dup", []string{"gastown/dup"}},
		{"gastown/witness", []string{"gastown/witness"}},
		{"mayor/", []string{"mayor/", "mayor"}},
	}
	for _, tt := range tests {
		m, err := r.GetMailbox(tt.address)
		if err != nil {
			t.Fatalf("GetMailbox(%q): %v", tt.address, err)
		}
		if got := m.assigneeVariants(); strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("assigneeVariants(%q) = %q, want %q", tt.address, got, tt.want)
		}
	}
}

// installSendCloseBd installs a fake bd that records the assignee of the mail
// it creates, serves that record on show, and enforces bd 1.3.0's exact close
// ownership check against it. Returns the close log path.
func installSendCloseBd(t *testing.T, townRoot string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake bd is POSIX-only")
	}
	beadsDir := filepath.Join(townRoot, ".beads")
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.db"), nil, 0644); err != nil {
		t.Fatalf("write beads.db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, ".gt-types-configured"), []byte(beads.TypeConfigSentinelValue()+"\n"), 0644); err != nil {
		t.Fatalf("write types sentinel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	stateDir := t.TempDir()
	assigneeFile := filepath.Join(stateDir, "assignee")
	closeLog := filepath.Join(stateDir, "close.log")
	script := `#!/bin/sh
sub=""
actor_flag=""
assignee=""
prev=""
for a in "$@"; do
  case "$prev" in
    --assignee) assignee="$a" ;;
  esac
  case "$a" in
    --actor=*) actor_flag="${a#--actor=}" ;;
    --assignee=*) assignee="${a#--assignee=}" ;;
    -*) ;;
    *) if [ -z "$sub" ]; then sub="$a"; fi ;;
  esac
  prev="$a"
done
case "$sub" in
  list|config|init) echo "[]" ;;
  create)
    printf '%s' "$assignee" > "` + assigneeFile + `"
    echo "hq-msg1"
    ;;
  show)
    stored=$(cat "` + assigneeFile + `")
    printf '[{"id":"hq-msg1","title":"Hello","description":"body","status":"open","priority":2,"assignee":"%s","created_at":"2026-10-01T12:00:00Z","labels":["gt:message","from:gastown/witness"]}]\n' "$stored"
    ;;
  close)
    stored=$(cat "` + assigneeFile + `")
    actor="$actor_flag"
    [ -z "$actor" ] && actor="$BEADS_ACTOR"
    [ -z "$actor" ] && actor="$BD_ACTOR"
    printf '%s\n' "$actor" >> "` + closeLog + `"
    if [ "$actor" != "$stored" ]; then
      echo "Error: cannot close hq-msg1: assignee is \"$stored\", actor is \"$actor\"; reclaim or use --force to override" >&2
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
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BEADS_ACTOR", "")
	return closeLog
}

// TestWorkerMailOwnership_SendThenClose sends mail through the router and
// closes the actually stored record: the addressed worker session owns it,
// while a same-rig same-name worker of the other kind does not.
func TestWorkerMailOwnership_SendThenClose(t *testing.T) {
	tests := []struct {
		name, to, bdActor string
		wantOwner         bool
	}{
		{"polecat owns mail sent to rig/name", "gastown/Toast", "gastown/polecats/Toast", true},
		{"crew owns mail sent to rig/name", "gastown/max", "gastown/crew/max", true},
		{"polecat owns mail sent to its qualified address", "gastown/polecats/dup", "gastown/polecats/dup", true},
		{"crew owns mail sent to its qualified address", "gastown/crew/dup", "gastown/crew/dup", true},
		{"same-name crew cannot close polecat mail", "gastown/polecats/dup", "gastown/crew/dup", false},
		{"same-name polecat cannot close crew mail", "gastown/crew/dup", "gastown/polecats/dup", false},
		{"ambiguous rig/name mail is owned by neither polecat", "gastown/dup", "gastown/polecats/dup", false},
		{"ambiguous rig/name mail is owned by neither crew", "gastown/dup", "gastown/crew/dup", false},
		{"other worker cannot close", "gastown/Toast", "gastown/crew/max", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			townRoot := makeWorkerTown(t)
			closeLog := installSendCloseBd(t, townRoot)
			t.Setenv("BD_ACTOR", tt.bdActor)

			r := NewRouterWithTownRoot(townRoot, townRoot)
			msg := &Message{From: "gastown/witness", To: tt.to, Subject: "Hello", Body: "body", SuppressNotify: true}
			if err := r.Send(msg); err != nil {
				t.Fatalf("send to %q: %v", tt.to, err)
			}

			m, err := r.GetMailbox(tt.bdActor)
			if err != nil {
				t.Fatalf("GetMailbox(%q): %v", tt.bdActor, err)
			}
			err = m.Delete("hq-msg1")
			if tt.wantOwner && err != nil {
				t.Fatalf("delete by %q of mail to %q: %v", tt.bdActor, tt.to, err)
			}
			if !tt.wantOwner {
				if err == nil {
					t.Fatalf("delete by %q of mail to %q succeeded, want ownership rejection", tt.bdActor, tt.to)
				}
				if !strings.Contains(err.Error(), "cannot close") {
					t.Errorf("unexpected error: %v", err)
				}
			}
			actors := readCloseActors(t, closeLog)
			if len(actors) != 1 || actors[0] != tt.bdActor {
				t.Errorf("close acted as %q, want unchanged caller %q (no ownership override)", actors, tt.bdActor)
			}
		})
	}
}
