# villains — go-api gRPC example

A small "villains" service exposing **no HTTP business API at all** —
only gRPC (`pkg/grpcx`), plus the HTTP health/metrics endpoints every
go-api service gets from `pkg/app` regardless. It exists to demonstrate
go-api's interservice communication patterns; `examples/heroes` calls it
as a client.

Runs with no external dependencies — in-memory store, the same
HMAC-signed dev tokens `examples/heroes` uses (same env var, same
default secret, so a token minted by either service's `/dev/token`-style
mechanism is accepted by both).

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

## Files

| File | What it shows |
|---|---|
| `main.go` | Wiring: config, authn verifier, role registry + policy store, `app.New` with `GRPCEnabled: true`, registering the generated server |
| `villain.go` | The one domain type — no separate Create/Update Go structs; the generated request messages already are the input shape |
| `store.go` | In-memory store (not a `crud.Store` — villains has no HTTP CRUD surface) |
| `server.go` | `VillainServiceServer` implementation: authorize inline (no generic interceptor — see `docs/grpc.md`), map errors via `grpcx.Status` |
| `policies/villains.cedar` | RBAC (list/read/create/capture by role) + one ABAC forbid (can't capture twice) |

The proto contract lives at the repo root: `proto/villains/v1/villains.proto`,
generated into `gen/villains/v1/` via `buf generate` (see `docs/grpc.md`).
