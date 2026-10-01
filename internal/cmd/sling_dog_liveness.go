package cmd

import (
	"fmt"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/tmux"
)

// dogSessionProbe is the subset of tmux needed to decide whether a dog session
// may still be executing a hooked formula. Satisfied by *tmux.Tmux.
type dogSessionProbe interface {
	HasSession(name string) (bool, error)
	IsAgentAliveChecked(session string) (bool, error)
	GetSessionActivity(session string) (time.Time, error)
}

// dogHookLiveness classifies the session that owns an existing dog formula hook.
type dogHookLiveness int

const (
	// dogHookLive: the agent is running and has been active within the TTL.
	dogHookLive dogHookLiveness = iota
	// dogHookDead: the tmux session or its agent process is definitively gone.
	dogHookDead
	// dogHookExpired: the agent is running but idle past the session TTL.
	dogHookExpired
)

func (l dogHookLiveness) String() string {
	switch l {
	case dogHookLive:
		return "live"
	case dogHookDead:
		return "dead"
	case dogHookExpired:
		return "expired"
	default:
		return fmt.Sprintf("dogHookLiveness(%d)", int(l))
	}
}

var newDogSessionProbe = func() dogSessionProbe { return tmux.NewTmux() }

// dogHookLivenessTTL is the inactivity window after which a live dog session is
// treated as abandoned. It reuses the daemon's stale-working timeout so sling
// and the daemon agree on when a working dog is stuck.
var dogHookLivenessTTL = func(townRoot string) time.Duration {
	return config.LoadOperationalConfig(townRoot).GetDaemonConfig().StaleWorkingTimeoutD()
}

// classifyDogHookLiveness decides whether a dog session is definitively gone.
// Lookup failures return an error rather than "dead": a transient tmux or
// process-table failure must never be mistaken for session death (hq-yea).
// When the agent is alive but its activity cannot be read, it stays live.
func classifyDogHookLiveness(probe dogSessionProbe, sessionName string, ttl time.Duration, now time.Time) (dogHookLiveness, error) {
	hasSession, err := probe.HasSession(sessionName)
	if err != nil {
		return dogHookLive, fmt.Errorf("checking session %s: %w", sessionName, err)
	}
	if !hasSession {
		return dogHookDead, nil
	}

	agentAlive, err := probe.IsAgentAliveChecked(sessionName)
	if err != nil {
		return dogHookLive, fmt.Errorf("checking agent in session %s: %w", sessionName, err)
	}
	if !agentAlive {
		return dogHookDead, nil
	}

	if ttl <= 0 {
		return dogHookLive, nil
	}
	lastActivity, err := probe.GetSessionActivity(sessionName)
	if err != nil || lastActivity.IsZero() {
		return dogHookLive, nil
	}
	if now.Sub(lastActivity) > ttl {
		return dogHookExpired, nil
	}
	return dogHookLive, nil
}

// replaceStaleDogFormulaHook burns an existing hooked formula wisp on a dog
// that was freshly assigned the same formula, but only after the dog's session
// is confirmed dead or idle past the session TTL. A dog whose kennel state was
// cleared while its session kept running still owns that molecule; burning it
// would close steps that have not run yet.
//
// Returns replaced=false with no error when the session is live: the caller
// must keep the existing hook executable and not pour a duplicate wisp.
func replaceStaleDogFormulaHook(existing *beads.Issue, info *DogDispatchInfo, formulaWorkDir string) (bool, error) {
	sessionName := fmt.Sprintf("hq-dog-%s", info.DogName)
	liveness, err := classifyDogHookLiveness(newDogSessionProbe(), sessionName, dogHookLivenessTTL(info.townRoot), time.Now())
	if err != nil {
		return false, fmt.Errorf("cannot confirm dog %s finished hooked formula %s; refusing to replace it: %w",
			info.DogName, existing.ID, err)
	}
	if liveness == dogHookLive {
		return false, nil
	}

	if err := cleanupStaleDogFormulaWispFn(existing.ID, formulaWorkDir); err != nil {
		return false, fmt.Errorf("cleaning stale dog formula wisp %s (session %s): %w", existing.ID, liveness, err)
	}
	return true, nil
}
