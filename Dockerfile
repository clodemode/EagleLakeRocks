# Build a static binary, ship it on scratch. No runtime, no package manager,
# nothing to patch — which is the point: this app must survive years of neglect.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off: modernc.org/sqlite is pure Go, so the binary is fully static.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /eagle-lake-rocks .

FROM scratch
COPY --from=build /eagle-lake-rocks /eagle-lake-rocks
# TLS roots, for outbound HTTPS if it is ever needed.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV DATA_DIR=/data PORT=8080
EXPOSE 8080
ENTRYPOINT ["/eagle-lake-rocks"]
