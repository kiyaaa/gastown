package mail

import (
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/constants"
)

// Mail to a polecat or crew worker is routed by the identity "rig/name"
// (AddressToIdentity), but the worker's session acts as "rig/polecats/name" or
// "rig/crew/name" (BD_ACTOR). bd's owner-only operations (close, used by mail
// archive/delete) compare a record's assignee with the acting identity exactly,
// so worker mail stores the worker's qualified identity as its assignee
// (gs-7cz). The recipient session then owns its mail under bd's own exact
// check, and a same-named polecat and crew member in one rig remain distinct
// principals.
//
// Records assigned to the bare "rig/name" (legacy mail, or mail to a name held
// by both a polecat and a crew member) are ambiguous between the two and are
// deliberately not reconciled to either: closing them needs --force.

// qualifiedWorkerIdentity returns the qualified identity for an explicit
// polecat or crew address ("rig/polecats/name", legacy "rig/polecat/name",
// "rig/crew/name"), or "" for any other address.
func qualifiedWorkerIdentity(address string) string {
	parts := strings.Split(strings.TrimSuffix(address, "/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" || isReservedTownSubpath(address) {
		return ""
	}
	switch parts[1] {
	case constants.DirPolecats, constants.RolePolecat:
		return parts[0] + "/" + constants.DirPolecats + "/" + parts[2]
	case constants.DirCrew:
		return parts[0] + "/" + constants.DirCrew + "/" + parts[2]
	}
	return ""
}

// MailAssigneeIdentity returns the identity stored as the assignee of mail
// sent to address: the qualified worker identity when address names a polecat
// or crew worker unambiguously, otherwise the routing identity
// AddressToIdentity(address).
func (r *Router) MailAssigneeIdentity(address string) string {
	return r.mailAssigneeIdentity(address, AddressToIdentity(address))
}

// mailAssigneeIdentity returns the assignee for mail to address whose routing
// identity is toIdentity. An explicit polecat/crew address keeps its
// qualifier. A bare "rig/name" is qualified only when exactly one of
// rig/polecats/name and rig/crew/name exists in the town; otherwise (unknown,
// ambiguous, or not a worker) toIdentity is returned unchanged.
func (r *Router) mailAssigneeIdentity(address, toIdentity string) string {
	if owner := qualifiedWorkerIdentity(address); owner != "" && AddressToIdentity(owner) == toIdentity {
		return owner
	}
	if r.townRoot == "" || isReservedTownSubpath(toIdentity) {
		return toIdentity
	}
	rig, name, ok := strings.Cut(toIdentity, "/")
	if !ok || rig == "" || name == "" || strings.Contains(name, "/") {
		return toIdentity
	}
	switch name {
	case constants.RoleWitness, constants.RoleRefinery, constants.DirPolecats, constants.DirCrew:
		return toIdentity
	}
	var matches []string
	for _, dir := range []string{constants.DirPolecats, constants.DirCrew} {
		if dirExists(filepath.Join(r.townRoot, rig, dir, name)) {
			matches = append(matches, rig+"/"+dir+"/"+name)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return toIdentity
}
