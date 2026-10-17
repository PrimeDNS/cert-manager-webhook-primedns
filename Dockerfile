# The build runs natively on the build machine and cross-compiles for the target platform (no emulation).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o webhook -ldflags '-w' .

# distroless: CA certificates and a non-root user, no shell, no package manager; nothing has to run on the target platform.
FROM gcr.io/distroless/static:nonroot

COPY --from=build /workspace/webhook /usr/local/bin/webhook

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/webhook"]
