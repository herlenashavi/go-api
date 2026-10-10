# Interservice gRPC (`pkg/grpcx`)

go-api services talk to each other over gRPC. `pkg/grpcx` gives a gRPC
server the same observability/auth posture `pkg/app`'s HTTP stack has, and
a client that propagates what needs to cross the service boundary —
without a generic "authorize this RPC" interceptor, for the same reason
chi routes don't get one automatically either: a per-method
(action, resource) mapping belongs in each handler, not guessed by the
framework.

## Defining the contract

Protobuf + [buf](https://buf.build) generate the Go types and service
interfaces. The repo root has:

```
buf.yaml        # module config (proto/ is the one module)
buf.gen.yaml    # protoc-gen-go + protoc-gen-go-grpc, output to gen/
proto/villains/v1/villains.proto
gen/villains/v1/*.pb.go   # generated, committed
```

```shell
# one-time: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
#                       google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest \
#                       github.com/bufbuild/buf/cmd/buf@latest
buf lint      # catches, e.g., an RPC reusing another RPC's response type
buf generate  # regenerate gen/ after editing a .proto
```

## Server

```go
application, err := app.New(ctx, app.Options{
    Config:      cfg,
    Verifier:    verifier,       // same Verifier as the HTTP side, or nil
    GRPCEnabled: true,
    GRPCPort:    9091,
})
villainsv1.RegisterVillainServiceServer(application.GRPCServer, myServer)
application.ListenAndServe(ctx) // serves HTTP and gRPC together, shuts both down together
```

`app.New` builds the gRPC server via `grpcx.NewServer` with go-api's
interceptor chain, outermost first: **panic recovery** (a crashed handler
goroutine would otherwise take the whole process down — see
`recoveryInterceptor`'s doc comment), **request ID** propagation,
**access logging**, then **bearer-token authentication** (only if a
`Verifier` is set). OTel tracing/metrics are wired as a `stats.Handler`
(otelgrpc's current API, not an interceptor), using the *same*
`TracerProvider`/`MeterProvider` as the HTTP side — one telemetry
pipeline, not two.

**The standard gRPC health service is always reachable without a
token**, even on a server with a `Verifier` — the same reason
`health.Router`'s HTTP endpoints are mounted outside any auth middleware.
This was a real bug caught by actually running two services against each
other rather than only unit-testing each interceptor in isolation: the
fix and its regression test are `healthServiceMethodPrefix` and
`TestHealthExemptFromAuth` in `pkg/grpcx`.

When `Debug` is true (`config.Base.Debug`), server reflection is also
registered, so `grpcurl` works without a copy of your `.proto` — the gRPC
equivalent of only serving OpenAPI/docs in debug mode.

## Authorization inside an RPC handler

There's no `authz.Route`-equivalent for gRPC. Call the `Authorizer`
directly, same escape hatch `pkg/crud`'s hand-written routes use:

```go
func (s *VillainServer) CaptureVillain(ctx context.Context, req *pb.CaptureVillainRequest) (*pb.CaptureVillainResponse, error) {
    principal, ok := authn.FromContext(ctx)
    if !ok { return nil, grpcx.Status(problem.Unauthorized("")) }

    existing, ok := s.store.Get(req.Id)
    if !ok { return nil, grpcx.Status(problem.NotFound("Villain")) }

    attrs := authz.Attrs(map[string]any{"status": shortStatus(existing.Status)})
    if err := s.authorizer.Authorize(ctx, principal, "villain:capture",
        authz.ResourceRef{Type: "Villain", ID: req.Id}, attrs); err != nil {
        return nil, grpcx.Status(err)
    }
    ...
}
```

`grpcx.Status` maps a `*problem.Problem` (what `Authorizer.Authorize`,
`httpx.Validate`, and `problem`'s constructors all return) to the closest
gRPC code, so the same authz/validation logic produces the right error
shape on both sides of a service. See `examples/villains/server.go` and
its `policies/villains.cedar` for the full pattern, including the
row-level ABAC check ("can't capture an already-captured villain") that
needs the real row loaded first — identical reasoning to
`examples/heroes/retire.go`'s HTTP-side equivalent.

## Client

```go
client, err := grpcx.Dial("villains:9091", grpcx.Dependencies{
    TracerProvider: application.Telemetry.TracerProvider,
    MeterProvider:  application.Telemetry.MeterProvider,
})
villainClient := villainsv1.NewVillainServiceClient(client)
```

Every call through a `grpcx.Dial`-built connection carries:

- the calling context's request ID (`reqctx.RequestID`), so a request that
  crosses from heroes into villains shows the same ID in both services'
  logs;
- the calling context's bearer token, **only if one is there** via
  `authn.WithRawToken` — which `authn.Middleware` sets for every
  authenticated HTTP request automatically. Forwarding the actual caller's
  token means villains authenticates and authorizes as that same user,
  not as a separate "heroes-service" identity — see `authn.WithRawToken`'s
  doc comment for when that's and isn't the right trust decision.

Errors come back as ordinary gRPC status errors; map them to
`*problem.Problem` with `grpcx.Problem(err)` so an HTTP handler that calls
a gRPC service can `problem.Write(w, r, grpcx.Problem(err))` exactly like
it would for a local error:

```go
villain, err := villainsClient.GetVillain(ctx, &pb.GetVillainRequest{Id: id})
if err != nil {
    return nil, grpcx.Problem(err) // 404/403/401/etc, not a bare 500
}
```

(`httpx.Handle`'s error path already calls `problem.From`, which
recognizes a `*problem.Problem` returned this way — see
`examples/heroes/villains_client.go`'s `GetVillain` method, which does
exactly this.)

## What's not here

- **No streaming support.** Every interceptor in this package is unary
  (`grpc.UnaryServerInterceptor`/`UnaryClientInterceptor`); a streaming
  RPC would need its own interceptor wired via `grpc.StreamInterceptor`
  and isn't exercised by either example service.
- **No generic method→action mapping.** See the top of this doc and
  `pkg/grpcx`'s package doc — this is deliberate, not an oversight.
- **No mTLS / service-identity story.** `grpcx.Dial` connects without TLS
  by default (fine for same-cluster calls or a mesh that terminates TLS
  itself); a deployment that needs mutual TLS between services passes its
  own `grpc.WithTransportCredentials` via `Dial`'s `extraOpts`, and needs
  its own answer for "what identity does a service present when it calls
  another service on nobody's behalf" (a cron job, a reconciler) — token
  forwarding only covers the "acting on this user's behalf" case.
