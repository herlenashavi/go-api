# Multi-stage build for the villains example. Run from the repo root:
#   docker build -f examples/villains/Dockerfile -t villains-example .

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/villains-example ./examples/villains

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/villains-example ./villains-example
COPY --from=build /src/examples/villains/policies ./policies

EXPOSE 8081 9091
ENV HTTP_PORT=8081
ENV GRPC_PORT=9091
USER nonroot:nonroot
ENTRYPOINT ["/app/villains-example"]
