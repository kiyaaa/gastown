package beads

import (
	"os"
	"strings"

	"github.com/steveyegge/gastown/internal/constants"
)

// Role-only town identities are spelled two ways inside Gas Town: mail and
// escalation assignees use the address form ("mayor/", "deacon/"), while
// session BD_ACTOR uses the bare role ("mayor", "deacon"). bd compares an
// issue's assignee with the acting identity without stripping slashes, so its
// owner-only operations (close) reject a mayor or deacon session acting on its
// own records (hq-4vo).
//
// CanonicalActorIdentity is the single rule that reconciles the two spellings:
// role-only town identities canonicalize to their address form, matching
// mail.AddressToIdentity. Every other identity is returned unchanged, so rig,
// polecat, crew, witness, and refinery path components keep distinguishing
// actors.
func CanonicalActorIdentity(identity string) string {
	if IsRoleOnlyActorIdentity(identity) {
		return strings.TrimSuffix(identity, "/") + "/"
	}
	return identity
}

// IsRoleOnlyActorIdentity reports whether identity is a role-only town
// identity ("mayor", "mayor/", "deacon", "deacon/"): the only identities with
// more than one accepted spelling under CanonicalActorIdentity.
func IsRoleOnlyActorIdentity(identity string) bool {
	switch strings.TrimSuffix(identity, "/") {
	case constants.RoleMayor, constants.RoleDeacon:
		return true
	}
	return false
}

// SameActorIdentity reports whether a and b name the same actor under
// CanonicalActorIdentity. An empty identity never matches anything, so an
// absent actor can never acquire ownership of an assigned record.
func SameActorIdentity(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return CanonicalActorIdentity(a) == CanonicalActorIdentity(b)
}

// OwnerActorForAssignee returns the actor to present to bd for an owner-only
// operation on a record assigned to assignee. When actor and assignee are two
// spellings of the same canonical identity, the assignee's stored spelling is
// returned so bd accepts it; this keeps both slash-assigned ("mayor/") and
// bare-assigned ("mayor") records operable. Otherwise actor is returned
// unchanged and bd's ownership check still rejects a different actor.
func OwnerActorForAssignee(assignee, actor string) string {
	if assignee != actor && SameActorIdentity(assignee, actor) {
		return assignee
	}
	return actor
}

// ProcessActor returns the actor bd attributes this process's writes to when
// no --actor flag is given: BEADS_ACTOR, then the deprecated BD_ACTOR that Gas
// Town sessions set. Empty means bd falls back to git/user identity, which Gas
// Town does not reconcile.
func ProcessActor() string {
	if actor := os.Getenv("BEADS_ACTOR"); actor != "" {
		return actor
	}
	return os.Getenv("BD_ACTOR")
}
