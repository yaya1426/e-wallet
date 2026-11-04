# syntax=docker/dockerfile:1
# Builds one binary from cmd/${APP} into a distroless image (TDR-012, TDR-015).
# APP defaults to wallet, so platforms that build the root Dockerfile without build
# arguments (e.g. DigitalOcean App Platform) get the wallet service.

FROM golang:1.27 AS build
ARG APP=wallet
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test -n "${APP}" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${APP}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
