BIN     := bin/astral
# The version lives in internal/app/version.go, so `go build .` and
# `make install` can never report different numbers at each other.
VERSION ?= $(shell sed -n 's/^var version = "\(.*\)"/\1/p' internal/app/version.go)
PREFIX  := $(HOME)/.local

# -s -w drop the symbol table and DWARF data: the binary is around a third
# smaller, so there is less to read off disk at every launch. Panics still
# carry full Go stack traces; only external debuggers lose information.
# (-trimpath is deliberately left out: it invalidates every cached gotk4
# object, which turns the next build back into a five-minute one.)
LDFLAGS := -s -w -X 'astral/internal/app.version=$(VERSION)'
BINDIR  := $(PREFIX)/bin
APPDIR  := $(PREFIX)/share/applications
ICONDIR := $(PREFIX)/share/icons/hicolor/scalable/apps

.PHONY: build run test install uninstall clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

run: build
	./$(BIN)

test:
	go test ./...

# install builds the binary and installs the desktop entry and icon, so Astral
# shows up in the GNOME app search (Super key) with a proper name and icon.
install: build
	mkdir -p $(BINDIR) $(APPDIR) $(ICONDIR)
	cp $(BIN) $(BINDIR)/.astral.new && mv -f $(BINDIR)/.astral.new $(BINDIR)/astral
	cp assets/astral.svg $(ICONDIR)/io.github.astral.svg
	sed 's|@BIN@|$(BINDIR)/astral|' packaging/io.github.astral.desktop > $(APPDIR)/io.github.astral.desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk-update-icon-cache -f -t $(PREFIX)/share/icons/hicolor 2>/dev/null || true
	@echo "Astral installed — search 'Astral' from the Super/Activities menu."
	@# Astral is single-instance, so launching it again while a copy is open
	@# just raises that window and exits 0 — the new binary never runs. With no
	@# warning that looks exactly like a successful update that changed nothing.
	@if pgrep -x astral >/dev/null 2>&1; then \
		echo ""; \
		echo "  NOTE: Astral is already running, and will keep using the previous build."; \
		echo "        Quit it and open it again to pick this one up."; \
	fi

uninstall:
	rm -f $(BINDIR)/astral $(APPDIR)/io.github.astral.desktop $(ICONDIR)/io.github.astral.svg
	-update-desktop-database $(APPDIR) 2>/dev/null || true

clean:
	rm -rf bin/
