BINARY := wrack
VERSION := 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

TARGETS := \
	darwin/amd64 darwin/arm64 \
	linux/amd64 linux/arm64 \
	windows/amd64 windows/arm64

.PHONY: build release clean test

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) .

# release builds every target into dist/
release: clean
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		name=$(BINARY)-$$os-$$arch$$ext; \
		echo "building $$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$$name .; \
	done
	@cd dist && for f in *; do shasum -a 256 $$f > $$f.sha256; done
	@echo "--- dist ---"
	@ls -la dist/

test:
	go test ./...

clean:
	rm -rf dist/ $(BINARY)
