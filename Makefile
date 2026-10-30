VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test integration docs clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o osb ./cmd/osb

test: build
	go vet ./...
	go test ./...
	./osb validate -f examples/devbox/osb.yaml
	./osb validate -f examples/kitchen-sink/osb.yaml
	./osb test examples

# Runs the generated scripts in containers of every flavor (needs docker).
integration: build
	./osb test examples --run
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

clean:
	rm -rf osb out
