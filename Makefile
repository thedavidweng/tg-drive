BINARY_NAME=td
DIST_DIR=dist

.PHONY: all bootstrap build test test-race clean lint fmt fmt-check vet ci-local mod-tidy mod-tidy-check snapshot goreleaser-check run-doctor \
	gui-bindings gui-bindings-check gui-frontend-install gui-frontend-build gui-frontend-check gui-go-check check-gui gui-build

all: ci-local build

bootstrap:
	go mod tidy

build:
	mkdir -p $(DIST_DIR)
	go build -trimpath -o $(DIST_DIR)/$(BINARY_NAME) ./cmd/td

test:
	go test ./...

test-race:
	go test -race ./...

fmt:
	gofumpt -extra -w .

fmt-check:
	@test -z "$$(gofumpt -extra -l .)" || (echo "gofumpt: files need formatting:" && gofumpt -extra -l . && exit 1)

vet:
	go vet ./...

lint: fmt-check vet
	@command -v golangci-lint >/dev/null 2>&1 || (echo "golangci-lint not installed; run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2" && exit 1)
	golangci-lint run

ci-local: fmt-check vet test test-race

mod-tidy:
	go mod tidy

mod-tidy-check: mod-tidy
	git diff --exit-code -- go.mod go.sum

clean:
	rm -rf $(DIST_DIR)
	rm -f *.test

run-doctor: build
	./$(DIST_DIR)/$(BINARY_NAME) doctor

goreleaser-check:
	goreleaser check

# ---- desktop GUI (ADR 0031) ------------------------------------------------
# The GUI links the platform webview through cgo (GTK4 and WebKitGTK 6.0 on
# Linux), so these targets are kept out of ci-local. Wails' own Taskfiles
# assume main.go beside frontend/, which this layout does not have.
GUI_TAGS=gui
GUI_BINDINGS=frontend/bindings

gui-bindings:
	wails3 generate bindings -silent -f '-tags $(GUI_TAGS)' -ts -i -d $(GUI_BINDINGS) ./cmd/td-gui

gui-bindings-check: gui-bindings
	@git diff --exit-code -- $(GUI_BINDINGS) && test -z "$$(git ls-files --others --exclude-standard -- $(GUI_BINDINGS))" \
		|| (echo "frontend bindings are stale; run: make gui-bindings" && exit 1)

gui-frontend-install:
	cd frontend && bun install --frozen-lockfile

gui-frontend-build: gui-frontend-install
	cd frontend && bun run build

gui-frontend-check: gui-frontend-install
	cd frontend && bun run typecheck && bun run lint && bun run test

gui-go-check: gui-frontend-build
	go vet -tags $(GUI_TAGS) ./...
	go test -tags $(GUI_TAGS) ./internal/gui/... ./cmd/td-gui/... ./frontend

check-gui: gui-bindings-check gui-frontend-check gui-go-check

gui-build: gui-frontend-build
	mkdir -p $(DIST_DIR)
	go build -tags $(GUI_TAGS) -trimpath -o $(DIST_DIR)/td-gui ./cmd/td-gui

# Local snapshot skips cosign signing: keyless signing needs CI OIDC.
snapshot:
	goreleaser release --snapshot --clean --skip=sign
