#!/bin/bash

VERSION="${FGLPKG_VERSION:-4.2.6}"
BUILD="${FGLPKG_BUILD:-$(date +%Y%m%d%H%M%S)}"
LDFLAGS="-s -w -X github.com/4js-mikefolcher/fglpkg/internal/cli.Version=${VERSION} -X github.com/4js-mikefolcher/fglpkg/internal/cli.Build=${BUILD}"

# Static, pure-Go binaries for every target. Without this, `go build` enables
# cgo for whichever target matches the build host (linux-amd64 on the
# ubuntu-latest release runner), dynamically linking that binary against the
# runner's glibc and giving it a GLIBC version floor — 4.2.4's linux-amd64
# needed GLIBC_2.34 (the libpthread→libc merge) and would not start on RHEL 8
# (glibc 2.28). fglpkg has no cgo (no `import "C"`, no C deps), so CGO_ENABLED=0
# only switches `net` to the pure-Go resolver and drops the glibc floor. (GIS-541)
export CGO_ENABLED=0

echo "Building fglpkg v${VERSION} (build ${BUILD})"

# Linux ARM
GOOS=linux GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-linux-arm64 ./cmd/fglpkg

# Linux Intel
GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-linux-amd64 ./cmd/fglpkg

# Mac Apple Silicon
GOOS=darwin GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-darwin-arm64 ./cmd/fglpkg

# Mac Intel
GOOS=darwin GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-darwin-amd64 ./cmd/fglpkg

# Windows ARM
GOOS=windows GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-windows-arm64.exe ./cmd/fglpkg

# Windows Intel
GOOS=windows GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-windows-amd64.exe ./cmd/fglpkg

echo "Done. Built 6 binaries in ./bin/"
