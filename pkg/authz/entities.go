package authz

import (
	cedar "github.com/cedar-policy/cedar-go"
)

// EntityTypeUser is the Cedar entity type used for the authenticated
// principal. Reference it in policies as `principal == User::"<subject>"`
// for a specific user, though most policies should match on role instead.
const EntityTypeUser cedar.EntityType = "User"

// UserEntityUID returns the Cedar entity UID for a principal's subject.
func UserEntityUID(subject string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeUser, cedar.String(subject))
}

// EntityTypeService is the Cedar entity type used for a calling service
// authenticated via mTLS (authn.ServiceIdentity), as opposed to a human
// user authenticated via bearer token (EntityTypeUser). Reference it in
// policies as `principal == Service::"heroes"` for a specific service,
// or (more usually) match on role the same way a User policy would —
// TLSConfig.ServiceRoles grants roles to a service identity the same
// RoleRegistry hierarchy a User's roles resolve through.
const EntityTypeService cedar.EntityType = "Service"

// ServiceEntityUID returns the Cedar entity UID for a service identity's
// Name.
func ServiceEntityUID(name string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeService, cedar.String(name))
}

// ResourceRef identifies the thing an action is being performed on, in
// Cedar's (type, id) terms. Use "*" as the ID for collection-level actions
// (list, create) that are not about one specific instance.
type ResourceRef struct {
	Type string
	ID   string
}

// CollectionResource builds a ResourceRef for a collection-level action
// (e.g. listing or creating heroes), conventionally resourceType::"*".
func CollectionResource(resourceType string) ResourceRef {
	return ResourceRef{Type: resourceType, ID: "*"}
}

// EntityUID returns the Cedar entity UID this ResourceRef corresponds to.
func (r ResourceRef) EntityUID() cedar.EntityUID {
	return cedar.NewEntityUID(cedar.EntityType(r.Type), cedar.String(r.ID))
}

// buildEntities assembles the EntityMap for a single authorization
// request: the static role hierarchy (shared, immutable), the dynamic
// entity for the calling actor — a User (bearer-token principal) or a
// Service (mTLS peer identity), whichever authorizer.go's caller built —
// with Parents set to the roles it was granted, and the resource entity
// carrying whatever attributes the caller already knows.
//
// At the HTTP/gRPC-middleware layer that's usually nothing beyond (type,
// id) — resourceAttrs is empty and policies can only do RBAC ("does this
// role permit this action"). For row-level ABAC ("can this user edit
// *this* record"), call Authorizer.Check/CheckService from inside the
// handler once the real row is loaded, passing its attributes (owner,
// tenant, status, ...).
//
// Cedar's "in" operator walks the role entities' own Parents transitively,
// so `principal in Role::"staff"` matches a caller holding any role that
// descends from "staff" — see RoleRegistry.
func buildEntities(roles *RoleRegistry, who actor, resource ResourceRef, resourceAttrs cedar.RecordMap) cedar.EntityMap {
	entities := make(cedar.EntityMap, len(roles.entitiesMap())+2)
	for uid, e := range roles.entitiesMap() {
		entities[uid] = e
	}

	roleUIDs := make([]cedar.EntityUID, 0, len(who.roles))
	for _, role := range who.roles {
		roleUIDs = append(roleUIDs, RoleEntityUID(role))
	}

	entities[who.uid] = cedar.Entity{
		UID:        who.uid,
		Parents:    cedar.NewEntityUIDSet(roleUIDs...),
		Attributes: cedar.NewRecord(who.attrs),
	}

	resourceUID := resource.EntityUID()
	entities[resourceUID] = cedar.Entity{
		UID:        resourceUID,
		Attributes: cedar.NewRecord(resourceAttrs),
	}

	return entities
}
