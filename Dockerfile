# Run the build stage natively on the host arch and cross-compile to the target
# arch. Without --platform=$BUILDPLATFORM, buildx runs the whole Go build under
# QEMU emulation for linux/arm64, which is slow enough to blow the CI job's
# no-output timeout. Go cross-compiles natively, so GOARCH=$TARGETARCH on the
# native builder is fast.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY main.go main.go
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/hr-recovery-controller .

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/hr-recovery-controller /usr/local/bin/hr-recovery-controller
ENTRYPOINT ["/usr/local/bin/hr-recovery-controller"]
