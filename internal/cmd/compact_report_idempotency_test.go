package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveCompactReportDateUsesUTCCalendarDay(t *testing.T) {
	t.Parallel()
	pacific := time.FixedZone("UTC-7", -7*60*60)
	kiritimati := time.FixedZone("UTC+14", 14*60*60)

	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"last second of UTC day", time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC), "2026-09-30"},
		{"first second of next UTC day", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "2026-10-01"},
		{"local evening already next UTC day", time.Date(2026, 9, 30, 17, 30, 0, 0, pacific), "2026-10-01"},
		{"local morning still previous UTC day", time.Date(2026, 10, 1, 9, 0, 0, 0, kiritimati), "2026-09-30"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCompactReportDate(tc.now, "")
			if err != nil {
				t.Fatalf("resolveCompactReportDate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("date = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveCompactReportDateExplicitOverride(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	got, err := resolveCompactReportDate(now, "2026-09-30")
	if err != nil || got != "2026-09-30" {
		t.Fatalf("override = (%q, %v), want 2026-09-30", got, err)
	}
	for _, bad := range []string{"2026-9-30", "09/30/2026", "2026-02-30"} {
		if _, err := resolveCompactReportDate(now, bad); err == nil {
			t.Errorf("override %q accepted, want format error", bad)
		}
	}
}

func TestCompactReportIdempotencyKeyIsDeterministic(t *testing.T) {
	t.Parallel()
	if a, b := compactReportIdempotencyKey("2026-09-30"), compactReportIdempotencyKey("2026-09-30"); a != b {
		t.Fatalf("key not deterministic: %q vs %q", a, b)
	}
	if got := compactReportIdempotencyKey("2026-09-30"); got != "compaction-report:2026-09-30" {
		t.Fatalf("key = %q, want compaction-report:2026-09-30", got)
	}
	if compactReportIdempotencyKey("2026-09-30") == compactReportIdempotencyKey("2026-10-01") {
		t.Fatal("different dates produced the same key")
	}
}

func TestRunDailyDigestRepeatedInvocationCreatesOneReport(t *testing.T) {
	stub := setupStatefulCompactReportStubs(t)
	resetCompactReportFlags(t)
	setCompactReportClock(t, time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC))

	for i := 0; i < 3; i++ {
		if err := runDailyDigest(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	creates := stub.creates(t)
	if len(creates) != 1 {
		t.Fatalf("bd create calls = %d, want 1:\n%s", len(creates), strings.Join(creates, "\n"))
	}
	if !strings.Contains(creates[0], "--labels="+compactReportLabel+",compaction-report:2026-09-30") {
		t.Fatalf("create args missing persisted idempotency key: %s", creates[0])
	}
	if got := stub.mails(t); len(got) != 1 {
		t.Fatalf("mayor notifications = %d, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}
}

func TestRunDailyDigestRetryAfterCloseFailureReusesOpenReport(t *testing.T) {
	stub := setupStatefulCompactReportStubs(t)
	resetCompactReportFlags(t)
	compactReportDate = "2026-09-30"

	// First attempt: bead is created but auto-close fails, leaving it open.
	t.Setenv("BD_CLOSE_FAIL", "1")
	if err := runDailyDigest(); err == nil {
		t.Fatal("first run: want auto-close error, got nil")
	}
	// Retry after the partial failure must find the open bead.
	t.Setenv("BD_CLOSE_FAIL", "")
	if err := runDailyDigest(); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if got := stub.creates(t); len(got) != 1 {
		t.Fatalf("bd create calls = %d, want 1 (retry must reuse open report)", len(got))
	}
	if got := stub.mails(t); len(got) != 0 {
		t.Fatalf("mayor notifications = %d, want 0 after retry of claimed day", len(got))
	}
}

func TestRunDailyDigestRetryRecognizesLegacyUnlabeledReport(t *testing.T) {
	stub := setupStatefulCompactReportStubs(t)
	resetCompactReportFlags(t)
	compactReportDate = "2026-09-30"
	stub.seedLegacyReport(t, "hq-legacy", "2026-09-30")

	if err := runDailyDigest(); err != nil {
		t.Fatalf("runDailyDigest: %v", err)
	}
	if got := stub.creates(t); len(got) != 0 {
		t.Fatalf("bd create calls = %d, want 0 when a legacy report exists", len(got))
	}
	if got := stub.mails(t); len(got) != 0 {
		t.Fatalf("mayor notifications = %d, want 0 when a legacy report exists", len(got))
	}
}

func TestRunDailyDigestFailsClosedWhenLookupFails(t *testing.T) {
	stub := setupStatefulCompactReportStubs(t)
	resetCompactReportFlags(t)
	compactReportDate = "2026-09-30"
	t.Setenv("BD_LIST_FAIL", "1")

	err := runDailyDigest()
	if err == nil || !strings.Contains(err.Error(), "checking for existing compaction report") {
		t.Fatalf("error = %v, want idempotency lookup failure", err)
	}
	if got := stub.creates(t); len(got) != 0 {
		t.Fatalf("bd create calls = %d, want 0 after failed lookup", len(got))
	}
	if got := stub.mails(t); len(got) != 0 {
		t.Fatalf("mayor notifications = %d, want 0 after failed lookup", len(got))
	}
}

func TestRunDailyDigestNextUTCDayProducesNewReport(t *testing.T) {
	stub := setupStatefulCompactReportStubs(t)
	resetCompactReportFlags(t)

	// Both instants are expressed in a non-UTC zone to prove the local
	// offset does not decide the day: 16:59 UTC-7 is 23:59 UTC on 09-30,
	// 17:01 UTC-7 is 00:01 UTC on 10-01.
	pacific := time.FixedZone("UTC-7", -7*60*60)
	setCompactReportClock(t, time.Date(2026, 9, 30, 16, 59, 0, 0, pacific))
	for i := 0; i < 2; i++ {
		if err := runDailyDigest(); err != nil {
			t.Fatalf("day 1 run %d: %v", i+1, err)
		}
	}
	setCompactReportClock(t, time.Date(2026, 9, 30, 17, 1, 0, 0, pacific))
	for i := 0; i < 2; i++ {
		if err := runDailyDigest(); err != nil {
			t.Fatalf("day 2 run %d: %v", i+1, err)
		}
	}

	creates := stub.creates(t)
	if len(creates) != 2 {
		t.Fatalf("bd create calls = %d, want 2 (one per UTC day):\n%s", len(creates), strings.Join(creates, "\n"))
	}
	if !strings.Contains(creates[0], "compaction-report:2026-09-30") ||
		!strings.Contains(creates[1], "compaction-report:2026-10-01") {
		t.Fatalf("creates not keyed by consecutive UTC days:\n%s", strings.Join(creates, "\n"))
	}
	mails := stub.mails(t)
	if len(mails) != 2 ||
		!strings.Contains(mails[0], "Wisp Compaction: 2026-09-30") ||
		!strings.Contains(mails[1], "Wisp Compaction: 2026-10-01") {
		t.Fatalf("mayor notifications = %q, want one per UTC day", mails)
	}
}

// compactReportStub records calls made by the stateful bd/gt stubs.
type compactReportStub struct {
	stateDir  string
	createLog string
	mailLog   string
}

func (s compactReportStub) creates(t *testing.T) []string {
	t.Helper()
	return readStubLog(t, s.createLog)
}

func (s compactReportStub) mails(t *testing.T) []string {
	t.Helper()
	return readStubLog(t, s.mailLog)
}

// seedLegacyReport records a closed report bead that predates the
// idempotency label: discoverable by title only.
func (s compactReportStub) seedLegacyReport(t *testing.T, id, date string) {
	t.Helper()
	title := "Compaction Report " + date
	row := `[{"id":"` + id + `","title":"` + title + `","status":"closed"}]` + "\n"
	if err := os.WriteFile(filepath.Join(s.stateDir, "title-"+title), []byte(row), 0644); err != nil {
		t.Fatalf("seed legacy report: %v", err)
	}
}

func readStubLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func setCompactReportClock(t *testing.T, now time.Time) {
	t.Helper()
	old := compactReportNow
	compactReportNow = func() time.Time { return now }
	t.Cleanup(func() { compactReportNow = old })
}

// setupStatefulCompactReportStubs installs bd/gt stubs where bd create
// persists the new bead so later bd list --label/--title calls can find it,
// mirroring how retries observe prior partial state in the real database.
func setupStatefulCompactReportStubs(t *testing.T) compactReportStub {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script command stubs not supported on Windows")
	}
	binDir := t.TempDir()
	logDir := t.TempDir()
	stub := compactReportStub{
		stateDir:  t.TempDir(),
		createLog: filepath.Join(logDir, "create.log"),
		mailLog:   filepath.Join(logDir, "mail.log"),
	}

	bdScript := `#!/bin/sh
state="$BD_STATE_DIR"
case "$1" in
  list)
    [ -n "$BD_LIST_FAIL" ] && { echo 'list failed' >&2; exit 1; }
    for a in "$@"; do
      case "$a" in
        --label=*) f="$state/label-${a#--label=}" ;;
        --title=*) f="$state/title-${a#--title=}" ;;
      esac
    done
    if [ -n "$f" ] && [ -f "$f" ]; then cat "$f"; else printf '[]\n'; fi
    ;;
  create)
    printf '%s' "$*" | tr '\n' ' ' >> "$BD_CREATE_LOG"
    echo >> "$BD_CREATE_LOG"
    id="hq-r$(wc -l < "$BD_CREATE_LOG" | tr -d ' ')"
    for a in "$@"; do
      case "$a" in
        --title=*) title="${a#--title=}" ;;
        --labels=*) labels="${a#--labels=}" ;;
      esac
    done
    row="[{\"id\":\"$id\",\"title\":\"$title\",\"status\":\"open\"}]"
    old_ifs="$IFS"; IFS=','
    for l in $labels; do printf '%s\n' "$row" > "$state/label-$l"; done
    IFS="$old_ifs"
    printf 'warning: beads.role not configured (GH#2950).\n%s\n' "$id"
    ;;
  close)
    [ -n "$BD_CLOSE_FAIL" ] && { echo 'close failed' >&2; exit 1; }
    exit 0
    ;;
  *)
    echo "unexpected bd command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(bdScript), 0755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	gtScript := `#!/bin/sh
if [ "$1" = "compact" ]; then
  printf '{"promoted":[],"deleted":[],"skipped":0}\n'
  exit 0
fi
if [ "$1" = "mail" ]; then
  echo "$*" | tr '\n' ' ' >> "$MAIL_LOG"
  echo >> "$MAIL_LOG"
  exit 0
fi
echo "unexpected gt command: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "gt"), []byte(gtScript), 0755); err != nil {
		t.Fatalf("write fake gt: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BD_STATE_DIR", stub.stateDir)
	t.Setenv("BD_CREATE_LOG", stub.createLog)
	t.Setenv("MAIL_LOG", stub.mailLog)
	t.Setenv("BD_LIST_FAIL", "")
	t.Setenv("BD_CLOSE_FAIL", "")
	return stub
}
