# lys-desktop dev tasks.

default:
    @just --list

# Every gate CI runs: vet, lint, no-fused, test, race, build.
check: vet lint no-fused test race build

vet:
    go vet ./...

lint:
    golangci-lint fmt --diff
    golangci-lint run

no-fused:
    no-fused

test *args:
    go test ${LYS_TEST_ARGS--shuffle=on} ./... {{args}}

race:
    CGO_ENABLED=1 go test -race ${LYS_TEST_ARGS--shuffle=on} ./...

build:
    go build -o /dev/null ./...

fmt:
    golangci-lint fmt

release-patch:
    nix run github:stubbedev/lys-ci#release -- patch

release-minor:
    nix run github:stubbedev/lys-ci#release -- minor

release-major:
    nix run github:stubbedev/lys-ci#release -- major

# Move every lys module and flake input to its head, then the vendor hash.
bump-lys:
    nix run github:stubbedev/lys-ci#bump-lys
