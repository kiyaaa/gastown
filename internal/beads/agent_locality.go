package beads

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Agent bead locality policy (gs-ftg, gs-8hj, hq-ou3, hq-6cp).
//
// Agent beads do not all live in the same database:
//
//   - Rig-scoped roles (witness, refinery, crew, polecat) live in the OWNING
//     RIG database, where bd prefix routing, rig listings (gt polecat list,
//     scheduler capacity, witness patrol) and patrol state commands look for
//     them. Every lifecycle path (spawn, state updates, gt done checkpoints
//     and completion, refinery post-merge cleanup, nuke) resolves the same
//     rig-local copy.
//   - Town roles (mayor, deacon, dog) live in the TOWN database.
//
// Rig-scoped beads created before this policy existed may still have a
// legacy copy in the town database. They are handled in one direction only:
//
//   - Writes never land on a legacy town copy. When the rig home has no copy,
//     the legacy bead is first migrated into the rig home with its title,
//     description, labels and status intact, and the town copy is retired
//     (closed) so no stale shadow remains (MigrateLegacyAgentBead).
//   - Reads of a bead that has not been migrated yet see the legacy town copy
//     without mutating anything.
//   - When both copies exist, the rig-local copy always wins.
//
// `gt doctor --fix` migrates all legacy copies explicitly and retires town
// shadows of beads that already have a rig-local copy.

// rigLocalAgentRoles are the agent roles whose beads live in the owning rig DB.
var rigLocalAgentRoles = map[string]bool{
	"witness":  true,
	"refinery": true,
	"crew":     true,
	"polecat":  true,
}

// IsRigLocalAgentRole reports whether beads for the given agent role are
// stored in the owning rig database rather than the town database.
func IsRigLocalAgentRole(role string) bool {
	return rigLocalAgentRoles[role]
}

// IsRigLocalAgentBeadID reports whether the agent bead ID belongs to a
// rig-scoped role (witness, refinery, crew, polecat) and therefore must be
// stored in the owning rig database.
func IsRigLocalAgentBeadID(id string) bool {
	rig, role, _, ok := ParseAgentBeadID(id)
	return ok && rig != "" && IsRigLocalAgentRole(role)
}

// agentBeadTargetFor returns the wrapper bound to the canonical home database
// for agent bead id: the owning rig DB for rig-scoped roles, the town DB for
// everything else. Use it for creates; use agentBeadReadTarget or
// agentBeadWriteTarget for operations on a bead that may exist already.
func (b *Beads) agentBeadTargetFor(id string) *Beads {
	if b.agentTargetResolved {
		return b
	}
	if home := b.rigAgentBeadHome(id); home != nil {
		return home
	}
	return b.agentBeadTarget()
}

// agentBeadReadTarget is agentBeadTargetFor with the read-only legacy
// fallback for rig-scoped beads: when the rig home has no copy of id but the
// town DB does, the town wrapper is returned so readers still see the only
// copy. Writers must use agentBeadWriteTarget instead.
func (b *Beads) agentBeadReadTarget(id string) *Beads {
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

// agentBeadWriteTarget returns the canonical home wrapper for writing agent
// bead id. A legacy town-only copy of a rig-scoped bead is migrated into the
// home first, so writes always reach the one copy that rig listings read.
func (b *Beads) agentBeadWriteTarget(id string) (*Beads, error) {
	if b.agentTargetResolved {
		return b, nil
	}
	target := b.agentBeadTargetFor(id)
	if !target.rigAgentHome {
		return target, nil
	}
	if migrated, err := target.migrateLegacyAgentBead(id); err != nil && !migrated {
		return nil, err
	}
	return target, nil
}

// ForAgentBeadID returns a wrapper bound to the canonical home database of
// agent bead id, for generic Show/Update calls on an agent bead (labels such
// as done-intent and done checkpoints, or raw field reads). Like the agent
// write operations, it migrates a legacy town-only copy into the home first.
// If that migration fails the home wrapper is still returned, so callers see
// the failure as a missing bead instead of silently writing the town copy.
func (b *Beads) ForAgentBeadID(id string) *Beads {
	target := b.agentBeadTargetFor(id)
	if target.rigAgentHome {
		_, _ = target.migrateLegacyAgentBead(id)
	}
	return target
}

// ShowAgentBeadAtHome looks an agent bead up in its canonical home database
// only, without the legacy town fallback. Repair paths use it to decide
// whether the home copy must be (re)created.
func (b *Beads) ShowAgentBeadAtHome(id string) (*Issue, error) {
	return b.agentBeadTargetFor(id).Show(id)
}

// rigAgentBeadHome returns a wrapper bound to the owning rig database for a
// rig-scoped agent bead, or nil when id is not rig-scoped or no rig database
// other than the town DB can be determined (e.g. a town without rigs).
func (b *Beads) rigAgentBeadHome(id string) *Beads {
	if !IsRigLocalAgentBeadID(id) {
		return nil
	}

	townRoot := b.getTownRoot()
	homeDir := ""
	if townRoot != "" {
		homeDir = routedRigBeadsDir(townRoot, ExtractPrefix(id))
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

// AgentBeadHomeDir returns the canonical home beads directory of agent bead
// id for callers that run bd directly instead of going through a Beads
// wrapper: the owning rig's beads directory for rig-scoped roles, the town
// beads directory otherwise (or when the rig cannot be resolved).
func AgentBeadHomeDir(townRoot, id string) string {
	if IsRigLocalAgentBeadID(id) {
		if dir := routedRigBeadsDir(townRoot, ExtractPrefix(id)); dirExists(dir) {
			return dir
		}
	}
	return GetTownBeadsPath(townRoot)
}

// RigAgentBeadsDir returns the beads directory holding the rig-scoped agent
// beads (including polecats) of the rig whose bead prefix is prefix (without
// the trailing hyphen), falling back to the town beads directory when the
// prefix has no usable route.
func RigAgentBeadsDir(townRoot, prefix string) string {
	if dir := routedRigBeadsDir(townRoot, strings.TrimSuffix(prefix, "-")+"-"); dirExists(dir) {
		return dir
	}
	return GetTownBeadsPath(townRoot)
}

// routedRigBeadsDir returns the beads directory routes.jsonl assigns to
// prefix (with trailing hyphen), or "" when the prefix is unrouted.
func routedRigBeadsDir(townRoot, prefix string) string {
	rigPath := GetRigPathForPrefix(townRoot, prefix)
	if rigPath == "" {
		return ""
	}
	return ResolveBeadsDir(rigPath)
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

// ListAgentBeadsWithRigs lists agent beads from this wrapper's database plus
// every routed rig database in its town. Use it where a town-wide agent view
// is needed: rig-scoped agent beads (witness, refinery, crew, polecat) live in
// their owning rig database, so a town-only listing misses them. For a
// rig-scoped bead found in several databases the rig-local copy wins over a
// legacy town duplicate. Rig databases that cannot be listed are skipped.
func (b *Beads) ListAgentBeadsWithRigs() (map[string]*Issue, error) {
	agents, err := b.ListAgentBeads()
	if err != nil {
		return nil, err
	}
	townRoot := b.getTownRoot()
	if townRoot == "" {
		return agents, nil
	}
	routes, err := LoadRoutes(GetTownBeadsPath(townRoot))
	if err != nil {
		return agents, nil
	}
	seen := map[string]bool{b.getResolvedBeadsDir(): true}
	for _, route := range routes {
		if route.Path == "." {
			continue
		}
		rigBeads := ResolveBeadsDir(filepath.Join(townRoot, route.Path))
		if seen[rigBeads] || !dirExists(rigBeads) {
			continue
		}
		seen[rigBeads] = true
		rigAgents, rigErr := NewWithBeadsDir(filepath.Dir(rigBeads), rigBeads).ListAgentBeads()
		if rigErr != nil {
			continue
		}
		for id, issue := range rigAgents {
			agents[id] = issue
		}
	}
	return agents, nil
}
