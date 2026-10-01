package doctor

import (
	"github.com/steveyegge/gastown/internal/beads"
)

// agentBeadPresence describes where an expected agent bead was found.
type agentBeadPresence int

const (
	agentBeadMissing agentBeadPresence = iota
	agentBeadPresent
	// agentBeadTownOnly marks a rig-scoped agent bead (witness, refinery,
	// crew, polecat) that exists only in the town database. Rig listings and
	// patrol state commands do not see such beads, so it is reported as
	// misplaced (gs-ftg, gs-8hj, hq-ou3).
	agentBeadTownOnly
)

// agentBeadInventory records which agent beads exist in the town database and
// which exist in rig databases. Keeping the two apart lets the check tell a
// rig-local bead from a legacy town-only copy of the same ID.
type agentBeadInventory struct {
	townIssues map[string]*beads.Issue // issues table (has labels)
	townWisps  map[string]bool         // wisps table (ID only)
	rigIssues  map[string]*beads.Issue
	rigWisps   map[string]bool
}

func newAgentBeadInventory() *agentBeadInventory {
	return &agentBeadInventory{
		townIssues: make(map[string]*beads.Issue),
		townWisps:  make(map[string]bool),
		rigIssues:  make(map[string]*beads.Issue),
		rigWisps:   make(map[string]bool),
	}
}

// addTown loads agent beads from the town database.
func (inv *agentBeadInventory) addTown(bd *beads.Beads) {
	loadAgentBeadsInto(bd, inv.townIssues, inv.townWisps)
}

// addRig loads agent beads from a rig database.
func (inv *agentBeadInventory) addRig(bd *beads.Beads) {
	loadAgentBeadsInto(bd, inv.rigIssues, inv.rigWisps)
}

func loadAgentBeadsInto(bd *beads.Beads, issues map[string]*beads.Issue, wisps map[string]bool) {
	if agents, err := bd.ListAgentBeads(); err == nil {
		for id, issue := range agents {
			issues[id] = issue
		}
	}
	if wispIDs, _ := bd.ListWispIDs(); wispIDs != nil {
		for id := range wispIDs {
			wisps[id] = true
		}
	}
}

// lookup reports where the agent bead id exists. The returned issue is the
// issues-table record used for label validation; it is nil for wisp-only or
// missing beads.
//
// Rig-scoped beads (including polecats) only count as present when found in a
// rig database; a copy found only in town is agentBeadTownOnly. Town roles
// count as present in either database.
func (inv *agentBeadInventory) lookup(id string) (agentBeadPresence, *beads.Issue) {
	if issue, ok := inv.rigIssues[id]; ok {
		return agentBeadPresent, issue
	}
	if inv.rigWisps[id] {
		return agentBeadPresent, nil
	}

	townIssue, inTownIssues := inv.townIssues[id]
	if !inTownIssues && !inv.townWisps[id] {
		return agentBeadMissing, nil
	}
	if beads.IsRigLocalAgentBeadID(id) {
		return agentBeadTownOnly, townIssue
	}
	return agentBeadPresent, townIssue
}

// hasTownShadow reports whether rig-scoped agent bead id has a rig-local copy
// and also a live legacy copy in the town database. The rig-local copy is
// canonical, so the town copy is stale shadow state.
func (inv *agentBeadInventory) hasTownShadow(id string) bool {
	if !beads.IsRigLocalAgentBeadID(id) {
		return false
	}
	_, inRigIssues := inv.rigIssues[id]
	if !inRigIssues && !inv.rigWisps[id] {
		return false
	}
	_, inTownIssues := inv.townIssues[id]
	return inTownIssues || inv.townWisps[id]
}
