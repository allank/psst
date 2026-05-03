BINARY  := psst
GO      := go
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -X github.com/allank/psst/cmd.Version=$(VERSION)
GOFLAGS := -trimpath -ldflags "$(LDFLAGS)"

build: fetch-model
	$(GO) build $(GOFLAGS) -o $(BINARY) .

fetch-model:
	@if [ ! -f internal/embedder/model/model.onnx ] || [ ! -f internal/embedder/model/tokenizer.json ]; then \
		echo "Downloading ONNX model (~90MB)..."; \
		mkdir -p internal/embedder/model; \
		curl -fsSL "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model.onnx" \
			-o internal/embedder/model/model.onnx; \
		curl -fsSL "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/tokenizer.json" \
			-o internal/embedder/model/tokenizer.json; \
	fi

build-release: fetch-model
	$(GO) build $(GOFLAGS) -tags embedmodel -o $(BINARY) .

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY)-darwin-arm64 .

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY)-darwin-amd64 .

build-linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY)-linux-amd64 .

build-linux-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY)-linux-arm64 .

test:
	$(GO) test ./...
