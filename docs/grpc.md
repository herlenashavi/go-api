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

## Mutual TLS for service-to-service calls

Bearer-token auth (above) answers "which *user* is this call on behalf
of." mTLS answers a different question: "which *service* is calling" —
verified by the TLS handshake itself, not an assertion either side could
fabricate. `pkg/grpcx`'s `TLSConfig`/`MTLSStore` wire this up; the result
lands in context as `authn.ServiceIdentity`, the mTLS counterpart to
`authn.Principal`.

```go
mtls, err := grpcx.LoadMTLS(grpcx.TLSConfig{
    CertFile:          "/etc/certs/tls.crt", // this service's own cert
    KeyFile:           "/etc/certs/tls.key",
    CAFile:             "/etc/certs/ca.crt",  // trust root for verifying peers
    RequireClientCert: true,                  // reject callers with no cert
    ServiceRoles: map[string][]string{
        "heroes": {"trusted-caller"},          // roles aren't read from the cert
    },
})
if err != nil { ... }

// Hot reload: certs rotate (cert-manager, SPIRE, a renewal cron job) —
// a long-lived server shouldn't need a restart to pick up a renewed one.
go mtls.Watch(ctx, 30*time.Second, application.Logger)

server := grpcx.NewServer(grpcx.Dependencies{MTLS: mtls, Verifier: verifier})
```

```go
// Client side: same TLSConfig, typically loaded once and shared.
client, err := grpcx.Dial("villains:9091", grpcx.Dependencies{MTLS: mtls})
```

Both sides normally load the *same* `TLSConfig` shape (one cert/key pair
issued to this service, one shared CA file) — the common case is every
service in a trust domain issued a cert by the same internal CA.

**Inside a handler**, `authn.ServiceIdentityFromContext(ctx)` gets you
the caller's verified identity, and `Authorizer.CheckService`/
`AuthorizeService` evaluate Cedar policies against it — by exact name or
by role, exactly like a `User` principal:

```go
// policies/villains.cedar
permit (principal == Service::"heroes", action == Action::"villain:read", resource);
permit (principal in Role::"trusted-caller", action == Action::"villain:list", resource);
```

```go
svc, ok := authn.ServiceIdentityFromContext(ctx)
if !ok { return nil, grpcx.Status(problem.Unauthorized("")) }
if err := s.authorizer.AuthorizeService(ctx, svc, "villain:read", resource, nil); err != nil {
    return nil, grpcx.Status(err)
}
```

A single call can carry **both** an `authn.Principal` (forwarded user
token) and an `authn.ServiceIdentity` (mTLS peer) — the recommended
production shape for a user-initiated interservice call: mTLS proves
*which service* is calling, the forwarded token proves *which user* it's
calling on behalf of. Authorize against whichever (or both) a given
policy actually needs. A call with `ServiceIdentity` only and no
`Principal` is the right shape for a service acting on its own behalf —
a cron job, a reconciler — with no user to act for at all.

**What's asymmetric about reload**: the server's own leaf certificate
hot-reloads on every handshake (`GetConfigForClient`); so does a client's
own certificate (`GetClientCertificate`). The CA pool used to verify the
*peer* is loaded once, client-side, at `LoadMTLS` time — root-of-trust
material changes far less often than individual leaf certs, and a
`grpc.ClientConn`'s transport credentials can't be swapped after `Dial`
without forcing a reconnect anyway, so hot-reloading that pool wouldn't
take effect on a long-lived connection. If your deployment rotates the
CA itself, redial rather than expecting `Watch` to pick it up live.

See `pkg/grpcx/mtls_test.go` for this exercised over real TLS sockets
with real (test-generated) certificates: identity extraction and role
grants, a server requiring a client cert rejecting one with none,
rejecting a cert signed by an untrusted CA, and both the manual-`Reload`
and polling-`Watch` hot-reload paths actually taking effect on a new
handshake.

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
- **No CA rotation.** The client-side CA pool (for verifying the
  *server*) is fixed at `LoadMTLS` time, by design — see the mTLS
  section above. Leaf certificate rotation (the far more common case)
  is fully hot-reloadable on both sides.
- **No automatic mTLS↔Cedar role sync.** `TLSConfig.ServiceRoles` is a
  static map you maintain in Go, the mTLS equivalent of a JWT claim —
  nothing here integrates with a cert issuer (SPIRE, cert-manager) to
  derive roles automatically from how a certificate was issued.
