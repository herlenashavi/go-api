# heroes — go-api worked example

A small "heroes" API exercising every piece of go-api: config, structured
logging, tracing/metrics, authentication, Cedar-based RBAC+ABAC
authorization, generic CRUD, a hand-written non-CRUD route, and — calling
`examples/villains` — interservice gRPC (`pkg/grpcx`). Runs with **no
external dependencies** on its own — in-memory store, HMAC-signed dev
tokens instead of a real IdP — though the `/nemesis` route below needs
`examples/villains` running too.

## Run it

```shell
go run .
# listening on :8080 by default; override with HTTP_PORT
```

`DEBUG=true` is the default for this example (see `main.go`) — it enables
pretty behavior for local dev and the `/dev/token` endpoint below. Set
`DEBUG=false` to see the service in its production posture.

## Try it

Mint a token for a role (dev-only endpoint, gated on `DEBUG=true`):

```shell
ADMIN=$(curl -s -X POST localhost:8080/dev/token \
  -d '{"subject":"alice","roles":["admin"]}' | jq -r .token)

VIEWER=$(curl -s -X POST localhost:8080/dev/token \
  -d '{"subject":"bob","roles":["viewer"]}' | jq -r .token)
```

Create and list (admin can create; viewer cannot — enforced by
`policies/heroes.cedar`, not by an `if` in the handler):

```shell
curl -s localhost:8080/v1/heroes/ -H "Authorization: Bearer $VIEWER"
# {"data":[],"meta":{"offset":0,"limit":20}}

curl -s -X POST localhost:8080/v1/heroes/ -H "Authorization: Bearer $VIEWER" \
  -d '{"name":"Clark","superPower":"flight"}'
# 403 forbidden

curl -s -X POST localhost:8080/v1/heroes/ -H "Authorization: Bearer $ADMIN" \
  -d '{"name":"Clark","superPower":"flight"}'
# 201, returns the created hero with a server-assigned id
```

Filter/sort (allowlisted — try `?bogus=x` and see a 400, not a silent
no-op):

```shell
curl -s "localhost:8080/v1/heroes/?status=active&sort=-createdAt&count=true" \
  -H "Authorization: Bearer $VIEWER"
```

The hand-written `retire` route, and the row-level (ABAC) policy that
stops you retiring an already-retired hero:

```shell
ID=$(curl -s localhost:8080/v1/heroes/ -H "Authorization: Bearer $ADMIN" | jq -r '.data[0].id')
curl -s -X POST localhost:8080/v1/heroes/$ID/retire -H "Authorization: Bearer $ADMIN"
curl -s -X POST localhost:8080/v1/heroes/$ID/retire -H "Authorization: Bearer $ADMIN"
# second call: 403 — policies/heroes.cedar's `forbid ... when { resource.status == "retired" }`
```

The nemesis route — heroes calling villains over gRPC (start
`examples/villains` first; `VILLAINS_GRPC_ADDR` defaults to
`localhost:9091`):

```shell
VID=$(grpcurl -plaintext -H "authorization: Bearer $ADMIN" \
  -d '{"name":"Lex Luthor","evilPower":"intellect"}' \
  localhost:9091 villains.v1.VillainService/CreateVillain | jq -r .villain.id)

curl -s -X POST localhost:8080/v1/heroes/ -H "Authorization: Bearer $ADMIN" \
  -d "{\"name\":\"Superman\",\"superPower\":\"flight\",\"nemesisVillainId\":\"$VID\"}"

HID=$(curl -s localhost:8080/v1/heroes/ -H "Authorization: Bearer $ADMIN" | jq -r '.data[0].id')
curl -s localhost:8080/v1/heroes/$HID/nemesis -H "Authorization: Bearer $ADMIN"
# {"heroId":"...","heroName":"Superman","villain":{"id":"...","name":"Lex Luthor",...}}
```

Villains' own access log shows the *same* request ID this curl call
generated — `villains_client.go`'s `grpcx.Dial` forwards it, and the
caller's bearer token, automatically. See
[docs/grpc.md](../../docs/grpc.md).

Mutual TLS between heroes and villains is opt-in (same `MTLS_CERT_FILE`/
`MTLS_KEY_FILE`/`MTLS_CA_FILE` env vars on both services) — see
[examples/villains/README.md](../villains/README.md#trying-mtls-between-heroes-and-villains)
for the full steps.

Health and metrics:

```shell
curl -s localhost:8080/health/liveness
curl -s localhost:8080/health/readiness
curl -s localhost:8080/health/metrics | grep http_server
```

## Files

| File | What it shows |
|---|---|
| `main.go` | Wiring: config, authn verifier, role registry + policy store, `app.New`, mounting CRUD + a custom route |
| `hero.go` | Response/Create/Update types — the generic parameters `crud.Resource` needs |
| `store_memory.go` | A `crud.Store[Hero, CreateHero, UpdateHero]` implementation with zero dependencies |
| `store_pg.go` | The same interface backed by real `dbx`/pgx — used automatically when `DATABASE_URL` is set, see below |
| `migrations/` | This service's SQL schema (`heroes`, `hero_audit_log`), embedded and applied by `migrate/` |
| `migrate/` | A separate one-off binary that applies/rolls back `migrations/` against `DATABASE_URL` |
| `retire.go` | A hand-written route + row-level ABAC check (loads the row, then calls `Authorizer.Check` with its real attributes; the write is conditional on the status that was authorized, so a concurrent change is a 409) |
| `villains_client.go` | `pkg/grpcx.Dial`-based client for the villains service — tracing, request ID, and token forwarding |
| `main.go`'s `loadMTLS` | Opt-in mutual TLS for the villains client — see villains' README |
| `nemesis.go` | The HTTP route that calls villains over gRPC; authorizes `hero:read` itself (hand-written routes don't inherit crud's checks) and maps villains' errors via `grpcx.Problem` |
| `dev_token.go` | Debug-only token minting — never do this in a real service |
| `policies/heroes.cedar` | The actual Cedar policies enforced above |

## Running against Postgres instead

Set `DATABASE_URL` and `main.go` switches from `MemoryHeroStore` to the
real `dbx`/pgx-backed `PGHeroStore` (store_pg.go) automatically — nothing
to edit. `store_pg.go`'s `var _ crud.Store[...] = (*PGHeroStore)(nil)`
assertion means this only compiles because `PGHeroStore` actually
satisfies the same interface the memory store does.

Apply the schema first, with the separate `migrate` binary — a real
service runs this as a deploy step, not something the service does to
itself on every boot:

```shell
docker run -d --name heroes-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=heroes -p 5432:5432 postgres:16-alpine

export DATABASE_URL="postgres://postgres:postgres@localhost:5432/heroes?sslmode=disable"
go run ./migrate up      # applies migrations/0001_create_heroes, 0002_create_hero_audit_log
go run .                 # now runs on PGHeroStore instead of MemoryHeroStore
```

`go run ./migrate down` rolls the schema back (local dev/tests only).
Under Tilt, this is wired up for you — see
[docs/tilt-dev.md](../../docs/tilt-dev.md#postgres--migrations).
