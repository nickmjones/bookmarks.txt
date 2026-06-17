# bookmarks.txt — build/install the bmtui terminal UI.
# The capture service (bm.py) needs no build; run it with `python3 bm.py`.

BINDIR ?= $(HOME)/.local/bin

.PHONY: build install uninstall test clean

build: ## Build the bmtui binary into ./bmtui/bmtui
	cd bmtui && go build -o bmtui .

install: build ## Install bmtui onto your PATH (BINDIR, default ~/.local/bin)
	mkdir -p $(BINDIR)
	install -m755 bmtui/bmtui $(BINDIR)/bmtui
	@echo "installed bmtui -> $(BINDIR)/bmtui"

uninstall: ## Remove the installed bmtui binary
	rm -f $(BINDIR)/bmtui

test: ## Run the Go test suite
	cd bmtui && go test ./...

clean: ## Remove the built binary
	rm -f bmtui/bmtui
