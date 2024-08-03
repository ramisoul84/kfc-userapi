FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/userapi ./cmd/userapi

FROM alpine:3.22
# Alpine already has CA certificates and busybox wget; time zones are
# compiled into the binary (-tags timetzdata), so no packages needed.
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/userapi /usr/local/bin/userapi
ENV APP_ENV=staging
EXPOSE 8001 9102
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -qO- http://127.0.0.1:8001/health || exit 1
ENTRYPOINT ["/usr/local/bin/userapi"]
