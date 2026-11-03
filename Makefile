# paqet build system.
#
#   make build            build for the host platform (./.build/paqet)
#   make install          build for host + install to GOBIN (re-signs on macOS,
#                         required after overwriting an existing binary, otherwise
#                         the kernel SIGKILLs it for an invalid code signature)
#   make build-all        darwin + linux/arm64 + linux/amd64 artifacts
#   make test vet clean   standard Go chores
#
# Version stamping (same -X flags as .github/workflows/build.yml):
#   make install VERSION=v1.0.0-alpha.21-hs3
# Defaults to `git describe`, falling back to the hardcoded base.
#
# Linux cross builds need zig (CGO) and a static per-arch libpcap, both
# bootstrapped automatically via `make pcap-deps` (runs as a prerequisite).

MODULE        := paqet
CMD           := ./cmd/main.go
BUILD_DIR     := .build

VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo v1.0.0-alpha.21)
GIT_TAG       ?= $(VERSION)
GIT_COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME    ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

LDFLAGS       := -s -w -buildid= \
	-X '$(MODULE)/cmd/version.Version=$(VERSION)' \
	-X '$(MODULE)/cmd/version.GitTag=$(GIT_TAG)' \
	-X '$(MODULE)/cmd/version.GitCommit=$(GIT_COMMIT)' \
	-X '$(MODULE)/cmd/version.BuildTime=$(BUILD_TIME)'
GCFLAGS       := all=-l=4
# Fully static Linux link flags (musl + static libpcap, no glibc runtime).
LINUX_LDFLAGS := $(LDFLAGS) -linkmode external -extldflags '-static'

GOBIN         ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN         := $(shell go env GOPATH)/bin
endif

# --- libpcap for Linux cross builds ---------------------------------------
LIBPCAP_VER   := 1.10.5
LIBPCAP_URL   := https://www.tcpdump.org/release/libpcap-$(LIBPCAP_VER).tar.gz
PCAP_ROOT     := $(BUILD_DIR)/libpcap
PCAP_SRC      := $(PCAP_ROOT)/libpcap-$(LIBPCAP_VER)
PCAP_TARBALL  := $(PCAP_ROOT)/libpcap-$(LIBPCAP_VER).tar.gz
PCAP_CFG      := --disable-shared --disable-dbus --disable-bluetooth --disable-netmap --disable-rdma

.PHONY: all build build-all install clean test vet pcap-deps version
.PHONY: $(BUILD_DIR)/paqet-darwin-arm64 $(BUILD_DIR)/paqet-linux-arm64 $(BUILD_DIR)/paqet-linux-amd64

all: build

version:
	@echo "$(VERSION) ($(GIT_COMMIT))"

# Host build. CGO is required (gopacket/pcap).
build:
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" \
		-o $(BUILD_DIR)/paqet $(CMD)
	@echo "built $(BUILD_DIR)/paqet [$(VERSION)]"

# Install to GOBIN. macOS invalidates the old code signature when a binary
# is overwritten in place, so strip xattrs and re-sign adhoc first.
install: build
	@mkdir -p $(GOBIN)
	cp $(BUILD_DIR)/paqet $(GOBIN)/paqet
ifeq ($(shell go env GOOS),darwin)
	xattr -c $(GOBIN)/paqet
	codesign --force --sign - $(GOBIN)/paqet
endif
	@echo "installed $(GOBIN)/paqet [$(VERSION)]"
	@$(GOBIN)/paqet version | head -2

# --- Linux cross builds (zig CGO + static libpcap + static musl) -----------------
# Targets use zig's musl libc plus `-static` so the resulting binaries have
# no dynamic glibc runtime dependency (fully static, libpcap included).
build-all: $(BUILD_DIR)/paqet-darwin-arm64 $(BUILD_DIR)/paqet-linux-arm64 $(BUILD_DIR)/paqet-linux-amd64
	@echo "all artifacts [$(VERSION)]:"; ls -la $(BUILD_DIR)/paqet-*

$(BUILD_DIR)/paqet-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	@if [ "$$(go env GOHOSTOS)" != "darwin" ]; then echo "error: paqet-darwin-arm64 CGO build requires a darwin host (got $$(go env GOHOSTOS)); refusing to use an incompatible host compiler" >&2; exit 1; fi
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
	go build -trimpath -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" \
		-o $@ $(CMD)

$(PCAP_TARBALL):
	@mkdir -p $(PCAP_ROOT)
	curl -sSL -o $@ $(LIBPCAP_URL)

# $1 = configure host triplet / build dir suffix, $2 = zig target
define pcap_static
	@if [ ! -f $(PCAP_SRC)-$1/libpcap.a ]; then \
		rm -rf $(PCAP_SRC)-$1 && mkdir -p $(PCAP_SRC)-$1 && \
		tar xzf $(PCAP_TARBALL) -C $(PCAP_SRC)-$1 --strip-components=1 && \
		cd $(PCAP_SRC)-$1 && \
		CC="zig cc -target $2" ./configure --host=$1 $(PCAP_CFG) >/dev/null && \
		make -j8 >/dev/null && \
		rm -f libpcap.a && \
		zig ar rc libpcap.a *.o && \
		zig ranlib libpcap.a && \
		echo "libpcap static [$1] ready"; \
	fi
endef

$(BUILD_DIR)/paqet-linux-arm64: $(PCAP_TARBALL)
	@mkdir -p $(BUILD_DIR)
	$(call pcap_static,aarch64-linux-musl,aarch64-linux-musl)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
	CC="zig cc -target aarch64-linux-musl" \
	CGO_CFLAGS="-I$(CURDIR)/$(PCAP_SRC)-aarch64-linux-musl" \
	CGO_LDFLAGS="-L$(CURDIR)/$(PCAP_SRC)-aarch64-linux-musl" \
	go build -trimpath -gcflags "$(GCFLAGS)" -ldflags "$(LINUX_LDFLAGS)" \
		-o $@ $(CMD)

$(BUILD_DIR)/paqet-linux-amd64: $(PCAP_TARBALL)
	@mkdir -p $(BUILD_DIR)
	$(call pcap_static,x86_64-linux-musl,x86_64-linux-musl)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
	CC="zig cc -target x86_64-linux-musl" \
	CGO_CFLAGS="-I$(CURDIR)/$(PCAP_SRC)-x86_64-linux-musl" \
	CGO_LDFLAGS="-L$(CURDIR)/$(PCAP_SRC)-x86_64-linux-musl" \
	go build -trimpath -gcflags "$(GCFLAGS)" -ldflags "$(LINUX_LDFLAGS)" \
		-o $@ $(CMD)

pcap-deps: $(PCAP_TARBALL)
	$(call pcap_static,aarch64-linux-musl,aarch64-linux-musl)
	$(call pcap_static,x86_64-linux-musl,x86_64-linux-musl)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)
