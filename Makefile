PYTHON ?= python
CORE_DIR ?= .work/sing-box

.PHONY: prepare build verify

prepare:
	$(PYTHON) scripts/prepare_core.py --output "$(CORE_DIR)"

build: prepare
	$(MAKE) -C "$(CORE_DIR)" build

verify: prepare
	cd "$(CORE_DIR)" && go mod tidy && git diff --exit-code -- go.mod go.sum
	cd "$(CORE_DIR)" && go build ./cmd/sing-box
