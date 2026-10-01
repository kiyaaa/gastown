package beads

import (
	"errors"
	"os"
	"path/filepath"
)

// Agent bead locality policy (gs-ftg, hq-ou3, hq-6cp).
//
// Agent beads do not all live in the same database:
//
//   - Rig-scoped persistent roles (witness, refinery, crew) live in the OWNING
//     RIG database. Patrol state commands (gt agents resolve, gt mol
//     await-signal/await-event, gt agent state) resolve these beads from the
//     rig-local store and refuse town-only copies.
//   - Polecat agent beads and town roles (mayor, deacon, dog) live in the TOWN
//     database (see ForAgentBead).
//
// Rig-scoped beads created before this policy existed may still have a
// legacy copy in the town database. Creates always target the rig home, so a
// create/repair of such a bead produces the rig-local copy and leaves the
// town copy untouched (resolvers prefer rig-local over town). Reads and
// updates of an existing bead fall back to the legacy town copy only when the
// rig home has no copy at all, so un-repaired towns keep working until
// `gt doctor --fix` creates the rig-local bead.

// rigLocalAgentRoles are the agent roles whose beads live in the owning rig DB.
var rigLocalAgentRoles = map[string]bool{
	"witness":  true,
	"refinery": true,
	"crew":     true,
}

// IsRigLocalAgentRole reports whether beads for the given agent role are
// stored in the owning rig database rather than the town database.
func IsRigLocalAgentRole(role string) bool {
	return rigLocalAgentRoles[role]
}

// IsRigLocalAgentBeadID reports whether the agent bead ID belongs to a
// rig-scoped persistent role (witness, refinery, crew) and therefore must be
// stored in the owning rig database.
func IsRigLocalAgentBeadID(id string) bool {
	rig, role, _, ok := ParseAgentBeadID(id)
	return ok && rig != "" && IsRigLocalAgentRole(role)
}

// agentBeadTargetFor returns the wrapper bound to the canonical home database
// for agent bead id: the owning rig DB for rig-scoped roles, the town DB for
// everything else. Use it for creates; use agentBeadTargetForExisting for
// operations on a bead that is expected to exist already.
func (b *Beads) agentBeadTargetFor(id string) *Beads {
	if b.agentTargetResolved {
		return b
	}
	if home := b.rigAgentBeadHome(id); home != nil {
		return home
	}
	return b.agentBeadTarget()
}

// agentBeadTargetForExisting is agentBeadTargetFor with the legacy fallback
// for rig-scoped beads: when the rig home has no copy of id but the town DB
// does, the town wrapper is returned so updates keep reaching the only copy.
func (b *Beads) agentBeadTargetForExisting(id string) *Beads {
	if b.agentTargetResolved {
		return b
	}
	target := b.agentBeadTargetFor(id)
	if !target.rigAgentHome {
		return target
	}
	if _, err := target.Show(id); err == nil || !errors.Is(err, ErrNotFound) {
		return target
	}
	legacy := target.townAgentBeadWrapper()
	if legacy == nil {
		return target
	}
	if _, err := legacy.Show(id); err != nil {
		return target
	}
	return legacy
}

// ShowAgentBeadAtHome looks an agent bead up in its canonical home database
// only, without the legacy town fallback. Repair paths use it to decide
// whether the home copy must be (re)created.
func (b *Beads) ShowAgentBeadAtHome(id string) (*Issue, error) {
	return b.agentBeadTargetFor(id).Show(id)
}

// rigAgentBeadHome returns a wrapper bound to the owning rig database for a
// rig-scoped agent bead, or nil when id is not rig-scoped or no rig database
// other than the town DB can be determined (preserving town placement).
func (b *Beads) rigAgentBeadHome(id string) *Beads {
	if !IsRigLocalAgentBeadID(id) {
		return nil
	}

	townRoot := b.getTownRoot()
	homeDir := ""
	if townRoot != "" {
		if rigPath := GetRigPathForPrefix(townRoot, ExtractPrefix(id)); rigPath != "" {
			homeDir = ResolveBeadsDir(rigPath)
		}
	}
	if !dirExists(homeDir) {
		// No usable route for the prefix (e.g. during rig bootstrap, a rig
		// whose beads dir is absent, or isolated tests): the caller's own
		// database is the best rig candidate.
		homeDir = b.getResolvedBeadsDir()
	}
	if !dirExists(homeDir) {
		return nil
	}
	if townRoot != "" && sameDir(homeDir, ResolveBeadsDir(GetTownBeadsPath(townRoot))) {
		return nil
	}

	home := &Beads{
		workDir:             filepath.Dir(homeDir),
		beadsDir:            homeDir,
		isolated:            b.isolated,
		serverPort:          b.serverPort,
		townRoot:            townRoot,
		noRoute:             true,
		agentTargetResolved: true,
		rigAgentHome:        true,
	}
	if sameDir(homeDir, b.getResolvedBeadsDir()) {
		home.store = b.store
	}
	home.townRootOnce.Do(func() {})
	return home
}

// townAgentBeadWrapper returns a resolved wrapper for the town database, or
// nil when the town root is unknown or is this wrapper's own database.
func (b *Beads) townAgentBeadWrapper() *Beads {
	townRoot := b.getTownRoot()
	if townRoot == "" {
		return nil
	}
	town := b.ForAgentBead()
	if sameDir(town.getResolvedBeadsDir(), b.getResolvedBeadsDir()) {
		return nil
	}
	town.store = nil // b.store is bound to b's database, not the town's
	town.agentTargetResolved = true
	return town
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
