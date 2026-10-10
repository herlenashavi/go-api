# Authorization (`pkg/authz`)

go-api's authorization is built on [Cedar](https://www.cedarpolicy.com/),
AWS's open-source policy language (via
[`cedar-policy/cedar-go`](https://github.com/cedar-policy/cedar-go)), rather
than role checks embedded in handler code. The goal: **roles are defined
once, policies for each API's actions are defined once, and both live as
data you can read, review, and change without touching Go code.**

## The three pieces

1. **Roles** (`authz.RoleRegistry`) — a small, static hierarchy you define
   in Go at startup, e.g. `admin > editor > viewer`. Each role becomes a
   Cedar entity (`Role::"admin"`) whose `Parents` are the roles it
   inherits from.
2. **Principals** (`authn.Principal`) — the authenticated caller, carrying
   `Roles []string` as asserted by your IdP (a JWT claim, typically).
3. **Policies** (`.cedar` files, loaded by `authz.LoadPolicyDir`) — `permit`
   and `forbid` statements over `(principal, action, resource)`, written
   against the roles above.

```go
roles, _ := authz.NewRoleRegistry(
    authz.Role{Name: "viewer"},
    authz.Role{Name: "editor", Parents: []string{"viewer"}},
    authz.Role{Name: "admin", Parents: []string{"editor"}},
)
policyStore, _ := authz.LoadPolicyDir("./policies")
authorizer := authz.New(policyStore, roles, logger)
```

```cedar
// editor (and admin, which inherits from editor) can update heroes.
permit (
  principal in Role::"editor",
  action == Action::"hero:update",
  resource
);
```

`principal in Role::"editor"` matches any principal holding a role that
**transitively descends** from `"editor"` — Cedar walks the role entities'
own `Parents` at evaluation time, so `admin` satisfies this check without a
second permit statement. See `pkg/authz/roles.go` and
`pkg/authz/authorizer_test.go` for the mechanics.

## Where enforcement happens

**Route-level (RBAC).** `authz.Middleware` (hand-written routes) and
`crud.Resource` (generated CRUD) both check `(principal, action,
resourceType+id)` *before* your handler runs. At this point the resource's
own data hasn't been loaded — only its type and ID are known — so this
layer answers "is this role allowed to perform this kind of action at
all," not "is this *specific* record editable by this user."

**Row-level (ABAC).** For checks that depend on the resource's actual
data (its owner, its status, its tenant), call `Authorizer.Check` or
`Authorizer.Authorize` directly from inside a handler, after loading the
row, passing its attributes via `authz.Attrs(...)`:

```go
hero, err := store.Get(ctx, id)
// ...
err = authorizer.Authorize(ctx, principal, "hero:retire",
    authz.ResourceRef{Type: "Hero", ID: id},
    authz.Attrs(map[string]any{"status": string(hero.Status)}),
)
```

```cedar
// a hero that's already retired cannot be retired again.
forbid (
  principal, action == Action::"hero:retire", resource
) when { resource.status == "retired" };
```

This is deliberately not automatic: inferring which attributes a policy
might need would mean either fetching every row before every route-level
check (expensive, and often impossible — list/create have no single row)
or reflecting over your domain types (the kind of magic this framework
avoids elsewhere). The full example lives in `examples/heroes/retire.go`
and `examples/heroes/policies/heroes.cedar`.

## Naming actions and resources

- **Action** = `"<lowercase resource>:<verb>"` by convention —
  `crud.Resource` defaults to this (`hero:list`, `hero:create`, ...);
  override via `Config.Actions` if your policies use different names.
- **Resource type** = whatever you pass as `ResourceType` (`"Hero"`), used
  as the Cedar entity type. Collection-level actions (list, create) use
  `resourceType::"*"` (`authz.CollectionResource`); instance-level actions
  use the record's real ID.

## Hot reload

`authz.PolicyStore.Reload()` re-parses the policy directory and atomically
swaps in the new `PolicySet` — in-flight requests keep using whatever
snapshot they already read, and a parse error never takes down the
previous, known-good policies. `PolicyStore.WatchAndReload(ctx, interval,
logger)` polls for changes if you want this without redeploying; most
services just restart on a policy change and never call it.

## What this is not

- **Not multi-tenant isolation by itself.** `Principal.TenantID` is
  attached to the principal entity's attributes (so a policy *can*
  reference `principal.tenantId`), but nothing automatically scopes a
  `Store`'s queries to a tenant — that's still the store's job (filter by
  tenant in SQL), with Cedar as an additional, explicit check if you want
  policy-driven tenant isolation too.
- **Not a replacement for authentication.** Cedar never sees an
  unauthenticated request — `authn.Middleware` runs first and populates
  the `Principal` that `authz` reads. A route with no `Verifier` has no
  `Principal`, and `Authorizer.Authorize` treats that as a hard 401, not
  an "allow everything."
