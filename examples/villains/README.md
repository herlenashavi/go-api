# villains — go-api gRPC example

A small "villains" service exposing **no HTTP business API at all** —
only gRPC (`pkg/grpcx`), plus the HTTP health/metrics endpoints every
go-api service gets from `pkg/app` regardless. It exists to demonstrate
go-api's interservice communication patterns; `examples/heroes` calls it
as a client.

Runs with no external dependencies by default — in-memory store, the
same HMAC-signed dev tokens `examples/heroes` uses (same env var, same
default secret, so a token minted by either service's `/dev/token`-style
mechanism is accepted by both). Set `DATABASE_URL` to run on real
Postgres instead — see "Running against Postgres instead" below.

## Run it

```shell
go run .
# HTTP (health only) on :8081, gRPC on :9091 — override with HTTP_PORT / GRPC_PORT
```

## Try it

villains has no HTTP endpoint of its own for minting tokens — run
`examples/heroes` alongside it (they share the dev secret, so a token
minted by heroes' `/dev/token` is accepted here) and get one from there:

```shell
ADMIN=$(curl -s -X POST localhost:8080/dev/token \
  -d '{"subject":"alice","roles":["admin"]}' | jq -r .token)
VIEWER_TOKEN=$(curl -s -X POST localhost:8080/dev/token \
  -d '{"subject":"bob","roles":["viewer"]}' | jq -r .token)
```

Then, with [grpcurl](https://github.com/fullstorydev/grpcurl)
(`go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest`) — server
reflection is on by default here since `DEBUG=true`:

```shell
grpcurl -plaintext localhost:9091 grpc.health.v1.Health/Check
# works with no token — see docs/grpc.md on why health is exempt

grpcurl -plaintext localhost:9091 villains.v1.VillainService/ListVillains
# Unauthenticated — no token

grpcurl -plaintext -H "authorization: Bearer $ADMIN" \
  -d '{"name":"Lex Luthor","evilPower":"intellect"}' \
  localhost:9091 villains.v1.VillainService/CreateVillain

VID=<id from the response above>

grpcurl -plaintext -H "authorization: Bearer $VIEWER_TOKEN" \
  -d "{\"id\":\"$VID\"}" localhost:9091 villains.v1.VillainService/CaptureVillain
# PermissionDenied — only admin can capture, per policies/villains.cedar

grpcurl -plaintext -H "authorization: Bearer $ADMIN" \
  -d "{\"id\":\"$VID\"}" localhost:9091 villains.v1.VillainService/CaptureVillain
# OK

grpcurl -plaintext -H "authorization: Bearer $ADMIN" \
  -d "{\"id\":\"$VID\"}" localhost:9091 villains.v1.VillainService/CaptureVillain
# PermissionDenied again — ABAC: can't capture an already-captured villain
```

## Calling it from heroes

Run `examples/heroes` with `VILLAINS_GRPC_ADDR=localhost:9091` (the
default, if both run on one machine), create a hero with
`nemesisVillainId` set to a villain's id, then:

```shell
curl localhost:8080/v1/heroes/<hero-id>/nemesis -H "Authorization: Bearer $ADMIN"
```

heroes looks the villain up over gRPC, forwarding your request ID and
token — villains' own logs show the same request ID heroes' did, and its
Cedar policies see the same principal, not a separate service identity.
See [docs/grpc.md](../../docs/grpc.md).

## Trying mTLS between heroes and villains

Mutual TLS is entirely opt-in here — unset, everything above works
exactly as shown. To turn it on:

```shell
go run ./../../deploy/mtls/gencerts -service heroes -service villains   # from this directory; prints the env vars below

MTLS_CERT_FILE=.../villains-cert.pem MTLS_KEY_FILE=.../villains-key.pem \
MTLS_CA_FILE=.../ca.pem MTLS_REQUIRE_CLIENT_CERT=true \
  go run .
```

and the matching `MTLS_CERT_FILE=.../heroes-cert.pem ...` env vars when
running `examples/heroes`. With `MTLS_REQUIRE_CLIENT_CERT=true`, a
`grpcurl` call with no client cert now fails with `tls: certificate
required` before it ever reaches an RBAC/ABAC check — try it with and
without `-cert`/`-key` flags to see the difference. See
[docs/grpc.md](../../docs/grpc.md#mutual-tls-for-service-to-service-calls).

## Running against Postgres instead

Set `DATABASE_URL` and `main.go` switches from the in-memory `VillainStore`
to the real `dbx`/pgx-backed `VillainPGStore` (`store_pg.go`)
automatically — nothing to edit. Both implement the same `villainStore`
interface `server.go` depends on (`server.go`'s
`var _ villainStore = (*VillainPGStore)(nil)` — mirrored by `store.go`'s
equivalent for the in-memory store — is what makes that swap safe to
make silently).

Apply the schema first, with the separate `migrate` binary — a real
service runs this as a deploy step, not something the service does to
itself on every boot:

```shell
docker run -d --name villains-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=villains -p 5432:5432 postgres:16-alpine

export DATABASE_URL="postgres://postgres:postgres@localhost:5432/villains?sslmode=disable"
go run ./migrate up      # applies migrations/0001_create_villains
go run .                 # now runs on VillainPGStore instead of VillainStore
```

`go run ./migrate down` rolls the schema back (local dev/tests only).
Under Tilt, this is wired up for you, on its own database alongside
heroes' — see
[docs/tilt-dev.md](../../docs/tilt-dev.md#postgres--migrations).

## Files

| File | What it shows |
|---|---|
| `main.go` | Wiring: config, authn verifier, role registry + policy store, `app.New` with `GRPCEnabled: true`, registering the generated server |
| `villain.go` | The one domain type — no separate Create/Update Go structs; the generated request messages already are the input shape |
| `store.go` | In-memory store (not a `crud.Store` — villains has no HTTP CRUD surface — but it does implement `villainStore`, see `server.go`) |
| `store_pg.go` | The same `villainStore` interface backed by real `dbx`/pgx — used automatically when `DATABASE_URL` is set, see above |
| `migrations/` | This service's SQL schema (`villains`), embedded and applied by `migrate/` |
| `migrate/` | A separate one-off binary that applies/rolls back `migrations/` against `DATABASE_URL` |
| `server.go` | `VillainServiceServer` implementation: authorize inline (no generic interceptor — see `docs/grpc.md`), map errors via `grpcx.Status` |
| `policies/villains.cedar` | RBAC (list/read/create/capture by role) + one ABAC forbid (can't capture twice) |
| `main.go`'s `loadMTLS` | Opt-in mutual TLS — see "Trying mTLS" above |

The proto contract lives right here: `proto/villains/v1/villains.proto`,
generated into `gen/villains/v1/` via `buf generate` run from this
directory (see `docs/grpc.md`).
