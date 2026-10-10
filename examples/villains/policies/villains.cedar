// Authorization policy for the villains example service. Same role
// hierarchy convention as examples/heroes (admin > editor > viewer) — a
// separate RoleRegistry, defined independently in this service's main.go,
// that happens to use the same names. Nothing shares these definitions
// across the two services; each service's policies are its own.

// viewer (and anyone above it) can list and read villains.
permit (
  principal in Role::"viewer",
  action in [Action::"villain:list", Action::"villain:read"],
  resource
);

// editor (and admin) can create new villains.
permit (
  principal in Role::"editor",
  action == Action::"villain:create",
  resource
);

// only admin can capture a villain.
permit (
  principal in Role::"admin",
  action == Action::"villain:capture",
  resource
);

// a villain that's already captured cannot be captured again — ABAC,
// depends on the resource's real `status` attribute, only available
// because VillainServer.CaptureVillain loads the row first. See that
// method's doc comment, and examples/heroes/policies/heroes.cedar for
// the identical pattern on the other service.
forbid (
  principal,
  action == Action::"villain:capture",
  resource
) when {
  resource.status == "captured"
};
