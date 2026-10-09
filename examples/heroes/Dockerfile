# Multi-stage build for the heroes example. Run from the repo root:
#   docker build -f examples/heroes/Dockerfile -t heroes-example .

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/heroes-example ./examples/heroes

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/heroes-example ./heroes-example
COPY --from=build /src/examples/heroes/policies ./policies

EXPOSE 8080
ENV HTTP_PORT=8080
USER nonroot:nonroot
ENTRYPOINT ["/app/heroes-example"]
