GO_VERSION := $(shell sed -n 's/^go //p' go.mod)

.PHONY: build test test-ci cover smoke fmt diagram
build:
	go build -o bin/loadtest ./cmd/loadtest
	go build -o bin/smoke-server ./cmd/smoke-server

test:
	go test -race -count=1 ./...

# The suite on the toolchain go.mod asks for rather than the one on your PATH, which is the version CI
# installs: a test that only passes on a newer one is not evidence.
test-ci:
	GOTOOLCHAIN=go$(GO_VERSION) go test -race -count=1 -timeout 120s ./...

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -l -w .

# A real run against a real server, which is the only way to know the exit code works.
smoke: build
	@./bin/smoke-server >/dev/null 2>&1 & server=$$!; \
	sleep 1; \
	./bin/loadtest -scenario examples/smoke.yaml -quiet; status=$$?; \
	kill $$server 2>/dev/null; \
	exit $$status

# Sources are HTML and Mermaid; a PNG is a build artifact.
diagram:
	@if command -v chromium >/dev/null 2>&1; then B=chromium; elif command -v google-chrome >/dev/null 2>&1; then B=google-chrome; else echo "no chromium on PATH: open docs/diagrams/*.html in a browser"; exit 0; fi; \
	for f in docs/diagrams/*.html; do $$B --headless --screenshot="$${f%.html}.png" --window-size=1200,1000 "$$f" && echo "wrote $${f%.html}.png"; done
