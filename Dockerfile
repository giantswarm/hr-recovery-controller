FROM golang:1.26 AS build
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY main.go main.go
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hr-recovery-controller .

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/hr-recovery-controller /usr/local/bin/hr-recovery-controller
ENTRYPOINT ["/usr/local/bin/hr-recovery-controller"]
