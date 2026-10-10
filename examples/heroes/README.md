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
| `store_pg.go` | The same interface backed by real `dbx`/pgx — **not wired up**, a reference for swapping stores |
| `retire.go` | A hand-written route + row-level ABAC check (loads the row, then calls `Authorizer.Check` with its real attributes) |
| `villains_client.go` | `pkg/grpcx.Dial`-based client for the villains service — tracing, request ID, and token forwarding |
| `nemesis.go` | The HTTP route that calls villains over gRPC; maps its errors via `grpcx.Problem` |
| `dev_token.go` | Debug-only token minting — never do this in a real service |
| `policies/heroes.cedar` | The actual Cedar policies enforced above |

## Running against Postgres instead

Swap the store construction in `main.go`:

```go
pool, err := dbx.NewPool(ctx, dbx.Config{DSN: os.Getenv("DATABASE_URL"), Tracer: application.Telemetry.Tracer})
store := NewPGHeroStore(pool)
```

`store_pg.go` has a `var _ crud.Store[...] = (*PGHeroStore)(nil)` assertion,
so this compiles iff `PGHeroStore` actually satisfies the interface — no
behavioral surprises from the swap. You'd also need a `heroes` table and
a migration tool (goose/atlas — not included); see its doc comment for
the expected schema shape.
