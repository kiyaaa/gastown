package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
)

type fakeDogSessionProbe struct {
	hasSession    bool
	hasSessionErr error
	agentAlive    bool
	agentErr      error
	activity      time.Time
	activityErr   error
}

func (f *fakeDogSessionProbe) HasSession(string) (bool, error) {
	return f.hasSession, f.hasSessionErr
}

func (f *fakeDogSessionProbe) IsAgentAliveChecked(string) (bool, error) {
	return f.agentAlive, f.agentErr
}

func (f *fakeDogSessionProbe) GetSessionActivity(string) (time.Time, error) {
	return f.activity, f.activityErr
}

const testDogHookTTL = 2 * time.Hour

func liveDogProbe(now time.Time) *fakeDogSessionProbe {
	return &fakeDogSessionProbe{hasSession: true, agentAlive: true, activity: now.Add(-30 * time.Second)}
}

func TestClassifyDogHookLiveness(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 52, 0, 0, time.UTC)
	transient := errors.New("context deadline exceeded")

	tests := []struct {
		name    string
		probe   *fakeDogSessionProbe
		ttl     time.Duration
		want    dogHookLiveness
		wantErr bool
	}{
		{name: "live session with recent activity", probe: liveDogProbe(now), ttl: testDogHookTTL, want: dogHookLive},
		{name: "live session past dispatcher stuck threshold but within TTL", probe: &fakeDogSessionProbe{hasSession: true, agentAlive: true, activity: now.Add(-29 * time.Minute)}, ttl: testDogHookTTL, want: dogHookLive},
		{name: "transient session lookup failure", probe: &fakeDogSessionProbe{hasSessionErr: transient}, ttl: testDogHookTTL, wantErr: true},
		{name: "transient agent lookup failure", probe: &fakeDogSessionProbe{hasSession: true, agentErr: transient}, ttl: testDogHookTTL, wantErr: true},
		{name: "unknown activity keeps live agent live", probe: &fakeDogSessionProbe{hasSession: true, agentAlive: true, activityErr: transient}, ttl: testDogHookTTL, want: dogHookLive},
		{name: "zero activity keeps live agent live", probe: &fakeDogSessionProbe{hasSession: true, agentAlive: true}, ttl: testDogHookTTL, want: dogHookLive},
		{name: "disabled TTL never expires live agent", probe: &fakeDogSessionProbe{hasSession: true, agentAlive: true, activity: now.Add(-48 * time.Hour)}, ttl: 0, want: dogHookLive},
		{name: "session gone", probe: &fakeDogSessionProbe{}, ttl: testDogHookTTL, want: dogHookDead},
		{name: "agent process dead", probe: &fakeDogSessionProbe{hasSession: true}, ttl: testDogHookTTL, want: dogHookDead},
		{name: "session idle past TTL", probe: &fakeDogSessionProbe{hasSession: true, agentAlive: true, activity: now.Add(-3 * time.Hour)}, ttl: testDogHookTTL, want: dogHookExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := classifyDogHookLiveness(tt.probe, "hq-dog-alpha", tt.ttl, now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("classifyDogHookLiveness() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyDogHookLiveness() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("classifyDogHookLiveness() = %v, want %v", got, tt.want)
			}
		})
	}
}

// stubDogHookReplacement swaps the session probe, TTL and wisp cleanup used by
// replaceStaleDogFormulaHook. It returns a pointer to the burned wisp IDs.
func stubDogHookReplacement(t *testing.T, probes ...dogSessionProbe) *[]string {
	t.Helper()
	prevProbe := newDogSessionProbe
	prevTTL := dogHookLivenessTTL
	prevCleanup := cleanupStaleDogFormulaWispFn
	t.Cleanup(func() {
		newDogSessionProbe = prevProbe
		dogHookLivenessTTL = prevTTL
		cleanupStaleDogFormulaWispFn = prevCleanup
	})

	calls := 0
	newDogSessionProbe = func() dogSessionProbe {
		probe := probes[len(probes)-1]
		if calls < len(probes) {
			probe = probes[calls]
		}
		calls++
		return probe
	}
	dogHookLivenessTTL = func(string) time.Duration { return testDogHookTTL }

	var burned []string
	cleanupStaleDogFormulaWispFn = func(wispID, _ string) error {
		burned = append(burned, wispID)
		return nil
	}
	return &burned
}

func freshAlphaAssignment() *DogDispatchInfo {
	return &DogDispatchInfo{
		DogName:       "alpha",
		townRoot:      "/town",
		workDesc:      "mol-dog-reaper",
		workStartedAt: time.Now(),
		ownsWork:      true,
	}
}

// Reproduces hq-yea: dog alpha's kennel state was cleared while its session was
// still executing mol-dog-reaper, and the next reaper dispatch re-assigned it.
// The live molecule must not be burned.
func TestReplaceStaleDogFormulaHookKeepsLiveSessionWork(t *testing.T) {
	burned := stubDogHookReplacement(t, liveDogProbe(time.Now()))
	existing := &beads.Issue{ID: "hq-wisp-g0ni"}

	replaced, err := replaceStaleDogFormulaHook(existing, freshAlphaAssignment(), "/town")
	if err != nil {
		t.Fatalf("replaceStaleDogFormulaHook() error = %v", err)
	}
	if replaced {
		t.Fatal("live dog session must keep its hooked formula")
	}
	if len(*burned) != 0 {
		t.Fatalf("burned %v while dog session was live", *burned)
	}
}

func TestReplaceStaleDogFormulaHookRefusesOnTransientProbeFailure(t *testing.T) {
	burned := stubDogHookReplacement(t, &fakeDogSessionProbe{hasSessionErr: errors.New("context deadline exceeded")})
	existing := &beads.Issue{ID: "hq-wisp-g0ni"}

	replaced, err := replaceStaleDogFormulaHook(existing, freshAlphaAssignment(), "/town")
	if err == nil {
		t.Fatal("transient liveness failure must surface as an error")
	}
	if !strings.Contains(err.Error(), "hq-wisp-g0ni") || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error should name the protected wisp and cause, got %v", err)
	}
	if replaced || len(*burned) != 0 {
		t.Fatalf("transient failure burned %v (replaced=%v)", *burned, replaced)
	}
}

// A first dispatch that hits a transient failure, followed by a retry that
// sees the dog alive, must leave the original molecule steps executable.
func TestReplaceStaleDogFormulaHookRetrySuccessLeavesWorkExecutable(t *testing.T) {
	burned := stubDogHookReplacement(t,
		&fakeDogSessionProbe{hasSession: true, agentErr: errors.New("tmux: server busy")},
		liveDogProbe(time.Now()),
	)
	existing := &beads.Issue{ID: "hq-wisp-g0ni"}

	if _, err := replaceStaleDogFormulaHook(existing, freshAlphaAssignment(), "/town"); err == nil {
		t.Fatal("first attempt should fail on transient probe error")
	}
	replaced, err := replaceStaleDogFormulaHook(existing, freshAlphaAssignment(), "/town")
	if err != nil {
		t.Fatalf("retry error = %v", err)
	}
	if replaced || len(*burned) != 0 {
		t.Fatalf("retry burned %v (replaced=%v); live work must stay executable", *burned, replaced)
	}
}

func TestReplaceStaleDogFormulaHookRecoversDeadOrExpiredSession(t *testing.T) {
	now := time.Now()
	for name, probe := range map[string]*fakeDogSessionProbe{
		"session gone":  {},
		"agent dead":    {hasSession: true},
		"idle past TTL": {hasSession: true, agentAlive: true, activity: now.Add(-testDogHookTTL - time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			burned := stubDogHookReplacement(t, probe)
			existing := &beads.Issue{ID: "hq-wisp-stale"}

			replaced, err := replaceStaleDogFormulaHook(existing, freshAlphaAssignment(), "/town")
			if err != nil {
				t.Fatalf("replaceStaleDogFormulaHook() error = %v", err)
			}
			if !replaced {
				t.Fatal("dead/expired dog session should allow replacing the stale hook")
			}
			if len(*burned) != 1 || (*burned)[0] != "hq-wisp-stale" {
				t.Fatalf("burned = %v, want [hq-wisp-stale]", *burned)
			}
		})
	}
}

func TestReplaceStaleDogFormulaHookPropagatesCleanupError(t *testing.T) {
	stubDogHookReplacement(t, &fakeDogSessionProbe{})
	cleanupStaleDogFormulaWispFn = func(string, string) error { return errors.New("close failed") }

	replaced, err := replaceStaleDogFormulaHook(&beads.Issue{ID: "hq-wisp-stale"}, freshAlphaAssignment(), "/town")
	if err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Fatalf("error = %v, want cleanup failure", err)
	}
	if replaced {
		t.Fatal("failed cleanup must not report replacement")
	}
}

func TestRunSlingFormulaGatesStaleDogHookOnSessionLiveness(t *testing.T) {
	body := runSlingFormulaSourceForTest(t)

	if strings.Contains(body, "cleanupStaleDogFormulaWispFn(") {
		t.Fatal("runSlingFormula must not burn an existing dog hook without the liveness gate")
	}
	gateIdx := strings.Index(body, "replaceStaleDogFormulaHook(existing, delayedDogInfo, formulaWorkDir)")
	cookIdx := strings.Index(body, "// Step 1: Cook the formula")
	if gateIdx == -1 || cookIdx == -1 || gateIdx > cookIdx {
		t.Fatal("stale dog hook replacement must be liveness-gated before cooking a new wisp")
	}
	gateBlock := body[gateIdx:cookIdx]
	if !strings.Contains(gateBlock, "delayedDogComplete = true") || !strings.Contains(gateBlock, "return nil") {
		t.Fatal("live dog hook must end dispatch as a no-op that keeps the dog assignment")
	}
}
