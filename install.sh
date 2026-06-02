#!/bin/sh
# pgvector-bench installer.
# Usage:  curl -fsSL https://rivestack.io/install.sh | sh
#
# Install dir selection:
#   1. $PGVB_INSTALL_DIR if set
#   2. $HOME/.local/bin if it's already in $PATH (no sudo needed)
#   3. /usr/local/bin (may require sudo)
#
# If the chosen dir isn't writable, the script falls back to sudo. If sudo
# can't get a terminal (common when piping through `curl | sh`), the script
# prints a clear next-step rather than failing opaquely.
set -eu

REPO="Rivestack/pgvector-bench"

uname_s=$(uname -s)
uname_m=$(uname -m)

case "$uname_s" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      echo "✗ unsupported OS: $uname_s" >&2; exit 1 ;;
esac

case "$uname_m" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "✗ unsupported arch: $uname_m" >&2; exit 1 ;;
esac

choose_install_dir() {
  if [ -n "${PGVB_INSTALL_DIR:-}" ]; then
    printf '%s' "$PGVB_INSTALL_DIR"
    return
  fi
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) printf '%s' "$HOME/.local/bin"; return ;;
  esac
  printf '%s' "/usr/local/bin"
}
INSTALL_DIR=$(choose_install_dir)
mkdir -p "$INSTALL_DIR" 2>/dev/null || true

echo "→ Fetching latest pgvector-bench for ${os}_${arch}..."

tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$tag" ]; then
  echo "✗ could not determine latest tag from GitHub" >&2
  exit 1
fi
version="${tag#v}"

asset="pgvector-bench_${version}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${tag}/${asset}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "→ Downloading $url"
if ! curl -fsSL "$url" -o "$tmp/$asset"; then
  echo "✗ download failed — does ${tag} include a build for ${os}_${arch}?" >&2
  exit 1
fi

if ! tar -xzf "$tmp/$asset" -C "$tmp"; then
  echo "✗ failed to extract $asset" >&2
  exit 1
fi

src="$tmp/pgvector-bench"
dst="$INSTALL_DIR/pgvector-bench"

if [ ! -f "$src" ]; then
  echo "✗ archive did not contain a 'pgvector-bench' binary (got: $(ls "$tmp"))" >&2
  exit 1
fi

# cp + chmod is more portable and gives clearer error messages than BSD/GNU install.
do_install() {
  cp "$src" "$dst" && chmod 0755 "$dst"
}

if [ -w "$INSTALL_DIR" ] || ([ ! -e "$INSTALL_DIR" ] && [ -w "$(dirname "$INSTALL_DIR")" ]); then
  do_install
else
  echo "→ $INSTALL_DIR is not writable, requesting sudo..."
  if ! command -v sudo >/dev/null 2>&1; then
    echo "✗ sudo not available. Re-run as root, or set PGVB_INSTALL_DIR to a writable path." >&2
    exit 1
  fi
  # -n: never prompt. We test this so we can give a clear next-step when
  # the script is being piped through `curl | sh` and stdin can't carry a
  # password prompt.
  if ! sudo -n true 2>/dev/null; then
    cat >&2 <<EOF
✗ sudo can't get a password through this pipe.

Two ways to finish the install:

  1) Pick a writable directory (recommended):
     curl -fsSL https://rivestack.io/install.sh | PGVB_INSTALL_DIR="\$HOME/.local/bin" sh
     # then make sure \$HOME/.local/bin is in your PATH

  2) Download to a temp file, then sudo install yourself:
     curl -fsSL https://rivestack.io/install.sh -o /tmp/install.sh
     sudo sh /tmp/install.sh
EOF
    exit 1
  fi
  sudo cp "$src" "$dst" && sudo chmod 0755 "$dst"
fi

echo
echo "✓ Installed pgvector-bench ${tag} to ${dst}"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) echo "  Try: pgvector-bench run --help" ;;
  *) echo "  Note: ${INSTALL_DIR} is not in your PATH. Add it, or invoke as ${dst}." ;;
esac
