// Authorization policy for the heroes example service.
//
// Roles form a hierarchy (see main.go's RoleRegistry): admin > editor >
// viewer. Cedar's `principal in Role::"x"` matches any role that
// transitively descends from "x", so an editor automatically gets
// everything a viewer gets, and an admin gets everything an editor gets,
// without repeating permit statements per role.

// viewer (and anyone above it) can list and read heroes.
permit (
  principal in Role::"viewer",
  action in [Action::"hero:list", Action::"hero:read"],
  resource
);

// editor (and admin) can create and update heroes.
permit (
  principal in Role::"editor",
  action in [Action::"hero:create", Action::"hero:update"],
  resource
);

// only admin can delete or retire a hero.
permit (
  principal in Role::"admin",
  action in [Action::"hero:delete", Action::"hero:retire"],
  resource
);

// a hero that's already retired cannot be retired again. This is the ABAC
// escape hatch in action: it depends on the resource's own `status`
// attribute, which route-level checks never have (they only know a
// resource's type and id, not its data) — only available because the
// "retire" handler loads the row first and passes its real attributes to
// Authorizer.Check explicitly. See retire.go.
forbid (
  principal,
  action == Action::"hero:retire",
  resource
) when {
  resource.status == "retired"
};
