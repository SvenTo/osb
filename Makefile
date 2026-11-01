VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test test-js integration docs site dist vscode vscode-test clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o osb ./cmd/osb

test: build
	go vet ./...
	go test ./...
	./osb validate -f examples/devbox/osb.yaml
	./osb validate -f examples/kitchen-sink/osb.yaml
	./osb test examples library

# Tests of the website's language support and the VS Code extension (needs node)
test-js: build vscode-files
	./osb actions list -o json > site/test/catalog.json
	node --test site/test/*.test.js
	cd editors/vscode && node --test test/*.test.js

# Runs the generated scripts in containers of every flavor (needs docker).
integration: build
	./osb test examples library --run
	for f in ubuntu:ubuntu:24.04 debian:debian:13 archlinux:archlinux:latest; do \
		flavor=$${f%%:*}; image=$${f#*:}; \
		for cfg in examples/devbox/osb.yaml examples/kitchen-sink/osb.yaml; do \
			echo "== $$cfg on $$flavor"; \
			./osb render shell --no-prompt -f $$cfg --flavor $$flavor | docker run --rm -i $$image sh -s >/dev/null || exit 1; \
		done; \
	done

docs: build
	./osb docs -o docs/reference
	./osb schema -o docs/osb.schema.json

# Static website with the WebAssembly build (out/site). Preview: make serve-site
site:
	rm -rf out/site
	mkdir -p out/site/js
	GOOS=js GOARCH=wasm go build -trimpath -ldflags "$(LDFLAGS)" -o out/site/osb.wasm ./cmd/osb-wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" out/site/js/
	go run ./site/gen -o out/site -version "$(VERSION)"

serve-site: site
	cd out/site && python3 -m http.server 8080

# Release archives and checksums (dist/)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
dist:
	rm -rf dist
	mkdir -p dist
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; dir=dist/osb_$${os}_$${arch}; \
		mkdir -p $$dir; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/osb ./cmd/osb || exit 1; \
		cp README.md $$dir/; \
		tar -C $$dir -czf $$dir.tar.gz osb README.md; \
		rm -r $$dir; \
	done
	cd dist && sha256sum *.tar.gz > checksums.txt

# VS Code extension: shared language support + catalog, packaged as dist/osb.vsix
vscode-files: build
	mkdir -p editors/vscode/lib editors/vscode/data
	cp site/static/js/osb-lang.js editors/vscode/lib/
	cp LICENSE editors/vscode/LICENSE
	./osb actions list -o json > editors/vscode/data/catalog.json

vscode: vscode-files
	mkdir -p dist
	cd editors/vscode && npx --yes @vscode/vsce@3 package -o ../../dist/osb.vsix

# Runs the extension in a real VS Code with a throw-away profile (needs code and
# xvfb-run). The fixture is restored afterwards.
vscode-test: vscode-files
	tmp=$$(mktemp -d) && cp editors/vscode/test/integration/fixture/osb.yaml $$tmp/osb.yaml.orig && \
	PATH="$$PWD:$$PATH" xvfb-run -a $${VSCODE:-/usr/share/code/code} --no-sandbox --disable-gpu --disable-extensions \
		--user-data-dir $$tmp/user --extensions-dir $$tmp/ext \
		--extensionDevelopmentPath=$$PWD/editors/vscode \
		--extensionTestsPath=$$PWD/editors/vscode/test/integration \
		editors/vscode/test/integration/fixture; rc=$$?; \
	cp $$tmp/osb.yaml.orig editors/vscode/test/integration/fixture/osb.yaml; rm -rf $$tmp; exit $$rc

clean:
	rm -rf osb out dist editors/vscode/lib editors/vscode/data editors/vscode/LICENSE
