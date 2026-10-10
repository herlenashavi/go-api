# Dev-loop image for Tilt — see heroes.Dockerfile's doc comment for why
# this is Alpine-based and copies a host-built binary rather than
# compiling in Docker. Not the production image (examples/villains/Dockerfile).
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY deploy/tilt/bin/villains /app/villains
WORKDIR /app
ENTRYPOINT ["/app/villains"]
