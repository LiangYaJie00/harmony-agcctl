VERSION ?= 0.1.0
BINARY := agcctl
BIN_DIR := bin
DIST_DIR := dist

.PHONY: build test vet dist clean

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="-X github.com/LiangYaJie00/harmony-agcctl/internal/version.Version=$(VERSION)" -o $(BIN_DIR)/$(BINARY) ./cmd/agcctl

test:
	go test ./...

vet:
	go vet ./...

dist:
	mkdir -p $(DIST_DIR)/package
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/LiangYaJie00/harmony-agcctl/internal/version.Version=$(VERSION)" -o $(DIST_DIR)/package/$(BINARY) ./cmd/agcctl
	cp README.md config.example.yaml $(DIST_DIR)/package/
	tar -C $(DIST_DIR)/package -czf $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64.tar.gz $(BINARY) README.md config.example.yaml
	sha=$$(shasum -a 256 $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64.tar.gz | awk '{print $$1}'); \
	printf '{\n  "version": "%s",\n  "payload": "%s",\n  "sha256": "%s"\n}\n' '$(VERSION)' "$$(pwd)/$(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64.tar.gz" "$$sha" > $(DIST_DIR)/MANIFEST.json
	@cat $(DIST_DIR)/MANIFEST.json

clean:
	go clean
	rm -rf $(BIN_DIR) $(DIST_DIR)
