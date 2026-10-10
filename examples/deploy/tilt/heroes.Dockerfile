# Dev-loop image for Tilt, NOT the production image (see
# examples/heroes/Dockerfile for that). The Tiltfile builds the binary on
# the host and copies only that binary in here — no compiler, no source
# — so this image builds in well under a second every time.
#
# Alpine, not distroless: Tilt's live_update restart_container() re-execs
# the entrypoint through a small shim it injects, which needs a shell
# (/bin/sh) in the image. A scratch/distroless image has none, which is
# exactly why examples/heroes/Dockerfile (which has no live-update need)
# can use distroless and this one can't.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY deploy/tilt/bin/heroes /app/heroes
WORKDIR /app
ENTRYPOINT ["/app/heroes"]
