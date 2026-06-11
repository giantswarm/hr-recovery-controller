# The Go binary is built by CircleCI (architect/go-build) and attached to the
# build context as <binary>-<os>-<arch>; this image only assembles the runtime.
# For a local build, produce the binary first:
#   CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o hr-recovery-controller-linux-amd64 .
FROM gcr.io/distroless/static:nonroot
USER 65532:65532
ARG TARGETOS
ARG TARGETARCH
COPY hr-recovery-controller-${TARGETOS}-${TARGETARCH} /usr/local/bin/hr-recovery-controller
ENTRYPOINT ["/usr/local/bin/hr-recovery-controller"]
