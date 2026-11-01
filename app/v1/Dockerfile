# One image per V1 service (SRS §9.1): a static binary on distroless, run as
# non-root (DR-001), buildable for linux/amd64 and linux/arm64 (DR-002), with
# the build SHA embedded (DR-003).
#
#   docker build --build-arg SERVICE=case-service --build-arg VERSION=$(git rev-parse --short HEAD) app/v1

FROM golang:1.26.3 AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG SERVICE
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath \
      -ldflags "-s -w -X github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform.Version=${VERSION}" \
      -o /out/app ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
