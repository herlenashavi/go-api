# Scaffolding (`go-api new`)

`cmd/go-api` generates a new, standalone Go service — its own `go.mod`
requiring `github.com/herlenashavi/go-api`, wired up per the flags you
pass. It is not a general templating engine: see "What `new` doesn't do
yet" below for the add-ons that are still roadmap items rather than
silently half-wired.

```
go install github.com/herlenashavi/go-api/cmd/go-api
go-api new orders -module github.com/acme/orders-svc -db -authn jwt -authz -crud Order -crud LineItem
cd orders
go mod tidy    # resolves github.com/herlenashavi/go-api — needs network access
go build ./...
```

## Flags

| Flag | Default | What it wires up |
|---|---|---|
| `-module` | *(required)* | the generated `go.mod`'s module path — guessing one would be wrong often enough to not be worth it |
| `-dir` | `./<name>` | target directory |
| `-authn` | `hmac` | `none` \| `hmac` \| `jwt` — which `authn.Verifier` `main.go` constructs. `hmac` also generates a debug-only `POST /dev/token` (see `examples/heroes/dev_token.go`'s pattern) so there's actually a way to get a bearer token without a real IdP. `jwt` wires `authn.NewJWTVerifier`; `JWKS_URL`/`JWT_ISSUER`/`JWT_AUDIENCE` are already env fields on `config.Base`, set at deploy time, not generation time |
| `-authz` | off | a Cedar `authz.Authorizer` + the standard viewer/editor/admin `RoleRegistry`, plus a starter `policies/<name>.cedar` granting list/read to viewer, create/update to editor, delete to admin — per resource |
| `-db` | off | a Postgres `dbx.Pool` + health check + `migrations/` + a `migrate` subcommand (same shape as `examples/heroes/migrate`) |
| `-crud <Name>` | none, repeatable | a CRUD resource: `<name>.go` (model + `Create<Name>`/`Update<Name>` request types — a minimal id/name/timestamps shape, not a guess at your domain), `store_<name>.go` (a Postgres-backed `crud.Store`), and a migration pair. **Requires `-db`** — generated resources are Postgres-backed only, by design (see "Why Postgres-only" below) |
| `-monorepo` | off | generate into the module enclosing `-dir` instead of a new standalone one — see "Monorepo mode" below |
| `-force` | off | overwrite a non-empty target directory |
| `-dry-run` | off | print the files that would be generated; write nothing |

## Monorepo mode

By default `go-api new` scaffolds a brand-new, standalone Go module —
its own `go.mod`. `-monorepo` instead generates *into* an existing one:

```
cd myrepo   # already has its own go.mod
go-api new orders -monorepo -dir services/orders -db -authn hmac -crud Order
```

What changes:

- **`-module` becomes optional.** Left unset, it's detected by walking
  up from `-dir` to the nearest `go.mod` (the same module boundary `go
  build` itself uses) and reading its `module` line. Given anyway, it
  must match the detected one exactly — `-monorepo` derives every
  internal import path from that module plus `-dir`'s position inside
  it, and an import path computed against a module `-dir` isn't actually
  inside can't be made to compile correctly, so a mismatch is rejected
  rather than silently producing the wrong path. If no `go.mod` is found
  at all, that's an error too: run `go mod init` at the repo root first,
  or drop `-monorepo` to scaffold a new standalone module instead.
- **No `go.mod` or `.gitignore` are generated.** A monorepo has one root
  `go.mod`; nesting another breaks it. The generated `README.md` says
  what to add to the root `.gitignore` instead, since this tool won't
  edit a file it didn't create.
- **The `migrate` binary's own import, and the `Dockerfile`, are shaped
  for a repo-root build context** instead of `-dir` being the module
  root — `go build -o /out/<name> ./<relative-dir>` and
  `docker build -f <relative-dir>/Dockerfile .` run from the repo root,
  the same pattern `examples/heroes/Dockerfile` already demonstrates in
  this repo (two services, one module, one `go.mod`).
- **CI is not touched.** The generated `README.md` names the build
  command to add to whatever pipeline the monorepo already has; nothing
  here edits `.github/workflows/*.yml` automatically, for the same
  "don't edit files this tool didn't create" reason as `.gitignore`.

## Why Postgres-only, no in-memory option

`examples/heroes` demonstrates both an in-memory and a Postgres-backed
store for the same resource (`MemoryHeroStore`/`PGHeroStore`) so the
*example* runs with zero setup. A generated service isn't a demo — it's
meant to become a real one — so `-crud` only ever generates the
Postgres-backed shape. `-db` without `-crud` (DB wiring only, no
resources yet) is still supported for a service that wants Postgres but
hand-writes its own routes.

This also keeps `-db` a plain boolean today without boxing in a future
multi-backend flag design. If a DynamoDB (or other) backend is added
later, `-db` becomes an enum (`-db=postgres|dynamo`) rather than two
independent booleans — not done now (see `docs/roadmap.md`), but worth
keeping in mind if you're about to script around the current boolean
flag in a way a flag-shape change would break.

## What `new` doesn't do yet

Deliberately out of scope for this first version — see `docs/roadmap.md`
for why each is deferred rather than silently half-wired:

- **gRPC / mTLS.** `app.Options.GRPCEnabled` and `grpcx`'s mTLS wiring
  aren't generated. A gRPC server with no `.proto`-generated service to
  register is a mostly-empty skeleton; worth doing once there's a clear
  answer for whether/how to scaffold a starter `.proto` too.
- **Tilt / k8s manifests.** `examples/deploy`'s pattern (Tiltfile entry +
  Postgres manifests) isn't generated.
- **The custom-metrics example.** The `Store`-decorator pattern from
  `docs/metrics.md` (`examples/heroes/metrics.go`) isn't generated —
  every service still gets the framework's own `http.server.*`/
  `db.client.*` metrics for free, just not a worked example of adding
  your own.
- **`go-api add resource`** for adding a CRUD resource to an *existing*
  generated service. `new` only scaffolds at creation time.

## Known limitation: naive pluralization

`-crud`'s route segment and table name come from a deliberately naive
English pluralizer (`Order` → `orders`, `Category` → `categories`, but
`Person` → `persons`). Rename the route, table, and migration by hand for
anything irregular — it's a generated starting point, not a contract.
