#!/bin/sh
# pgvector-bench installer.
# Usage: curl -fsSL https://rivestack.io/install.sh | sh
#
# Downloads the latest release for your OS/arch from GitHub and installs the
# binary into /usr/local/bin (or $PGVB_INSTALL_DIR if set).
set -eu

REPO="Rivestack/pgvector-bench"
INSTALL_DIR="${PGVB_INSTALL_DIR:-/usr/local/bin}"

uname_s=$(uname -s)
uname_m=$(uname -m)

case "$uname_s" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      echo "unsupported OS: $uname_s" >&2; exit 1 ;;
esac

case "$uname_m" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported arch: $uname_m" >&2; exit 1 ;;
esac

echo "→ Fetching latest pgvector-bench for ${os}_${arch}..."

tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$tag" ]; then
  echo "could not determine latest tag" >&2
  exit 1
fi
version="${tag#v}"

asset="pgvector-bench_${version}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "→ Downloading $url"
curl -fsSL "$url" -o "$tmp/$asset"
tar -xzf "$tmp/$asset" -C "$tmp"

if [ ! -w "$INSTALL_DIR" ]; then
  echo "→ $INSTALL_DIR is not writable, using sudo"
  sudo install -m 0755 "$tmp/pgvector-bench" "$INSTALL_DIR/pgvector-bench"
else
  install -m 0755 "$tmp/pgvector-bench" "$INSTALL_DIR/pgvector-bench"
fi

echo
echo "✓ Installed pgvector-bench ${tag} to ${INSTALL_DIR}/pgvector-bench"
echo "  Try: pgvector-bench run --help"
