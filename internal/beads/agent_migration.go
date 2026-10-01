package beads

import (
	"errors"
	"fmt"
	"strings"
)

// legacyAgentBeadRetireReason is recorded on a town copy of a rig-scoped agent
// bead when its rig-local copy becomes canonical (gs-8hj).
const legacyAgentBeadRetireReason = "superseded by rig-local agent bead (gs-8hj)"

// MigrateLegacyAgentBead moves a legacy town-only copy of the rig-scoped agent
// bead id into its owning rig database (see agent_locality.go).
//
// The rig-local copy carries the town copy's title, description (every
// lifecycle field, including ones this version does not parse), labels
// (done-intent, done checkpoints, safety stops, ...) and status. The town copy
// is then retired by closing it, so it can no longer act as a stale shadow;
// it is kept, not deleted, for audit.
//
// It reports whether a migration happened. Nothing is done when id is not a
// rig-scoped agent bead, the rig home already has a copy, or no legacy copy
// exists. When the rig-local copy was created but the town copy could not be
// retired, it returns true with the retire error: the rig copy is canonical
// either way.
func (b *Beads) MigrateLegacyAgentBead(id string) (bool, error) {
	home := b.agentBeadTargetFor(id)
	if !home.rigAgentHome {
		return false, nil
	}
	return home.migrateLegacyAgentBead(id)
}

// RetireLegacyAgentBeadShadow closes the town copy of a rig-scoped agent bead
// that already has a rig-local copy. The rig-local copy always wins, so the
// open town copy is only stale shadow state. It reports whether a shadow was
// retired; it never touches the town copy when the rig-local copy is missing.
func (b *Beads) RetireLegacyAgentBeadShadow(id string) (bool, error) {
	home := b.agentBeadTargetFor(id)
	if !home.rigAgentHome {
		return false, nil
	}
	if _, err := home.Show(id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	legacy := home.townAgentBeadWrapper()
	if legacy == nil {
		return false, nil
	}
	issue, err := legacy.Show(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if issue.Status == string(StatusClosed) {
		return false, nil
	}
	if err := legacy.CloseWithReason(legacyAgentBeadRetireReason, id); err != nil {
		return false, fmt.Errorf("retiring town copy of %s: %w", id, err)
	}
	return true, nil
}

// migrateLegacyAgentBead implements MigrateLegacyAgentBead on a wrapper that
// is already bound to the rig home of id.
func (b *Beads) migrateLegacyAgentBead(id string) (bool, error) {
	// Only a definite "not found" in the rig home triggers a migration. Any
	// other lookup error leaves the operation to run (and report its own
	// error) against the home, exactly as without a legacy copy.
	if _, err := b.Show(id); err == nil || !errors.Is(err, ErrNotFound) {
		return false, nil
	}
	legacy := b.townAgentBeadWrapper()
	if legacy == nil {
		return false, nil
	}
	legacyIssue, err := legacy.Show(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading legacy town copy of %s: %w", id, err)
	}

	fl, lockErr := b.lockAgentBead(id)
	if lockErr != nil {
		return false, fmt.Errorf("locking agent bead %s: %w", id, lockErr)
	}
	defer func() { _ = fl.Unlock() }()

	if _, err := b.createAgentBeadRecord(id, legacyIssue.Title, legacyAgentDescription(legacyIssue), legacyIssue.Labels); err != nil {
		// A concurrent writer may have migrated the bead first.
		if _, showErr := b.Show(id); showErr != nil {
			return false, fmt.Errorf("migrating legacy town copy of %s to %s: %w", id, b.getResolvedBeadsDir(), err)
		}
	}
	if status := legacyIssue.Status; status != "" && status != string(StatusOpen) {
		if err := b.Update(id, UpdateOptions{Status: &status}); err != nil {
			return true, fmt.Errorf("restoring status %q on migrated %s: %w", status, id, err)
		}
	}

	if legacyIssue.Status == string(StatusClosed) {
		return true, nil
	}
	if err := legacy.CloseWithReason(legacyAgentBeadRetireReason, id); err != nil {
		return true, fmt.Errorf("retiring town copy of %s: %w", id, err)
	}
	return true, nil
}

// legacyAgentDescription returns the description to carry into the rig-local
// copy. Very old beads may hold agent_state only in the structured column;
// it is written into the description so the state survives the move.
func legacyAgentDescription(issue *Issue) string {
	description := issue.Description
	if issue.AgentState == "" {
		return description
	}
	if fields := ParseAgentFields(description); fields != nil && fields.AgentState != "" {
		return description
	}
	if description != "" && !strings.HasSuffix(description, "\n") {
		description += "\n"
	}
	return description + "agent_state: " + issue.AgentState
}
