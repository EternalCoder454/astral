# The image CI runs in.
#
# CI used to start from a bare Fedora and spend most of its time doing the same
# two things on every run: installing GTK, libadwaita and a Go toolchain, then
# compiling gotk4 from cold. gotk4 is an enormous generated binding and that
# second part is most of the wait, every time, for code that had not changed.
#
# Both happen here instead, once, when this file or the module files change.
# What CI compiles is Astral.
FROM fedora:44

# The C libraries the binding is generated against, pinned to what Astral is
# developed against rather than whatever a distribution happens to ship: the
# generated sources call adw_about_dialog_add_other_app, which needs libadwaita
# 1.6, and an older one fails to compile before any Astral code is reached.
RUN dnf -y install --setopt=install_weak_deps=False \
        gtk4-devel libadwaita-devel gobject-introspection-devel \
        gcc pkgconf-pkg-config git tar gzip diffutils findutils \
    && dnf clean all

# Go from upstream, so the version here is the one go.mod asks for rather than
# whatever the distribution is on.
ARG GO_VERSION=1.26.0
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz

# Absolute cache paths, not $HOME-relative ones. A GitHub container job runs
# with HOME=/github/home, so a cache left in /root would be invisible to it and
# every byte of the warming below would be wasted.
ENV GOPATH=/opt/go \
    GOCACHE=/opt/gocache \
    GOMODCACHE=/opt/go/pkg/mod \
    GOTOOLCHAIN=local \
    PATH=/usr/local/go/bin:/opt/go/bin:$PATH

# The warming itself. Only the module files are copied in, so this layer is
# rebuilt when dependencies change and not when Astral does.
WORKDIR /warm
COPY go.mod go.sum ./
RUN go mod download

# A file importing everything expensive, built so the object code lands in the
# cache. Twice, because -race has its own cache entries and CI runs both.
RUN printf '%s\n' \
    'package warm' \
    '' \
    'import (' \
    '	_ "github.com/diamondburned/gotk4-adwaita/pkg/adw"' \
    '	_ "github.com/diamondburned/gotk4/pkg/cairo"' \
    '	_ "github.com/diamondburned/gotk4/pkg/core/glib"' \
    '	_ "github.com/diamondburned/gotk4/pkg/gdk/v4"' \
    '	_ "github.com/diamondburned/gotk4/pkg/gio/v2"' \
    '	_ "github.com/diamondburned/gotk4/pkg/glib/v2"' \
    '	_ "github.com/diamondburned/gotk4/pkg/gtk/v4"' \
    '	_ "github.com/diamondburned/gotk4/pkg/pango"' \
    '	_ "golang.org/x/image/bmp"' \
    '	_ "golang.org/x/image/draw"' \
    '	_ "golang.org/x/image/tiff"' \
    '	_ "golang.org/x/image/webp"' \
    '	_ "modernc.org/sqlite"' \
    ')' > warm.go \
    && go build ./... \
    && go build -race ./... \
    && go vet ./... \
    && rm -f warm.go

# staticcheck, so the lint job does not install it on every run either.
RUN go install honnef.co/go/tools/cmd/staticcheck@latest

# Readable by whatever user the job ends up running as.
RUN chmod -R a+rX /opt/gocache /opt/go && chmod -R a+w /opt/gocache

WORKDIR /
