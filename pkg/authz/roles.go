package authz

import (
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
)

// Role is a named grant that can be assigned to users (authn.Principal.Roles)
// and referenced from Cedar policies as `Role::"<name>"`. Parents lets you
// build a role hierarchy — e.g. "org-admin" inherits everything "editor"
// can do — without repeating permit statements: Cedar's `principal in
// Role::"editor"` matches any principal holding a role that transitively
// descends from "editor".
type Role struct {
	Name    string
	Parents []string
}

// RoleRegistry is the authoritative set of roles a service recognizes. It is
// built once at startup from your role definitions (typically a small Go
// literal, not user input) and turned into static Cedar entities that are
// merged into every authorization request's entity set.
type RoleRegistry struct {
	entities cedar.EntityMap
	names    map[string]struct{}
}

// EntityTypeRole is the Cedar entity type used for every role. Reference it
// in policies as, e.g., `principal in Role::"admin"`.
const EntityTypeRole cedar.EntityType = "Role"

// RoleEntityUID returns the Cedar entity UID for a role name.
func RoleEntityUID(name string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeRole, cedar.String(name))
}

// NewRoleRegistry validates and compiles the given roles. It fails if any
// role names a parent that is not itself defined, or if a name is repeated —
// both are config mistakes best caught at startup, not mid-request.
func NewRoleRegistry(roles ...Role) (*RoleRegistry, error) {
	names := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		if r.Name == "" {
			return nil, fmt.Errorf("authz: role with empty name")
		}
		if _, dup := names[r.Name]; dup {
			return nil, fmt.Errorf("authz: duplicate role %q", r.Name)
		}
		names[r.Name] = struct{}{}
	}
	for _, r := range roles {
		for _, parent := range r.Parents {
			if _, ok := names[parent]; !ok {
				return nil, fmt.Errorf("authz: role %q has undefined parent %q", r.Name, parent)
			}
		}
	}

	entities := make(cedar.EntityMap, len(roles))
	for _, r := range roles {
		parentUIDs := make([]cedar.EntityUID, 0, len(r.Parents))
		for _, parent := range r.Parents {
			parentUIDs = append(parentUIDs, RoleEntityUID(parent))
		}
		uid := RoleEntityUID(r.Name)
		entities[uid] = cedar.Entity{
			UID:     uid,
			Parents: cedar.NewEntityUIDSet(parentUIDs...),
		}
	}

	return &RoleRegistry{entities: entities, names: names}, nil
}

// Has reports whether name is a defined role.
func (rr *RoleRegistry) Has(name string) bool {
	_, ok := rr.names[name]
	return ok
}

// entitiesMap returns the static role entities, safe to merge into a
// per-request EntityMap (callers must not mutate the returned map directly;
// use merge helpers in entities.go).
func (rr *RoleRegistry) entitiesMap() cedar.EntityMap {
	return rr.entities
}
