#!/bin/sh
# Installs qoqi from the releases on GitHub:
#
#     curl -fsSL https://qoqi.dev/install.sh | sh
#
# QOQI_VERSION picks a release (default: the latest), QOQI_INSTALL the
# folder the binary goes in (default: ~/.local/bin).
#
# The download is checked against the checksums of its release. Those
# come from the same place, and say only that it arrived whole: where gh
# is installed, the binary is also checked against its attestation,
# signed by GitHub for the release workflow of the repository, which no
# one who changed the release could make. QOQI_VERIFY=1 requires that
# check, QOQI_VERIFY=0 skips it.
set -eu

repo="qoqiflow/qoqi"
version="${QOQI_VERSION:-latest}"
target="${QOQI_INSTALL:-$HOME/.local/bin}"
verify="${QOQI_VERIFY:-auto}"

fail() {
    echo "qoqi: $*" >&2
    exit 1
}

case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "no binary for $(uname -s); build it from the source: git clone https://github.com/$repo && cd qoqi && make build" ;;
esac
case "$(uname -m)" in
    x86_64 | amd64) arch=x86_64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "no binary for $(uname -m)" ;;
esac

name="qoqi-$os-$arch"
if [ "$version" = latest ]; then
    url="https://github.com/$repo/releases/latest/download/$name.tar.gz"
    sums="https://github.com/$repo/releases/latest/download/checksums.txt"
else
    url="https://github.com/$repo/releases/download/$version/$name.tar.gz"
    sums="https://github.com/$repo/releases/download/$version/checksums.txt"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "Downloading $url"
curl -fsSL "$url" -o "$work/$name.tar.gz" || fail "could not download $url"
curl -fsSL "$sums" -o "$work/checksums.txt" || fail "could not download $sums"

expected="$(grep " $name.tar.gz\$" "$work/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$work/$name.tar.gz" | cut -d' ' -f1)"
else
    actual="$(shasum -a 256 "$work/$name.tar.gz" | cut -d' ' -f1)"
fi
[ -n "$expected" ] && [ "$expected" = "$actual" ] || fail "the download does not match its checksum"

# Releases before v0.2.3 have no attestation (v0.2.2 was never built)
case "$version" in
    v0.1.* | v0.2.0 | v0.2.1 | v0.2.2) attested=false ;;
    *) attested=true ;;
esac
if [ "$verify" != 0 ]; then
    if ! command -v gh >/dev/null 2>&1; then
        [ "$verify" = 1 ] && fail "QOQI_VERIFY=1 needs gh, to check the attestation: https://cli.github.com"
        echo "Not checked against its attestation: install gh to have it checked"
    elif ! $attested; then
        [ "$verify" = 1 ] && fail "$version has no attestation: v0.2.3 and later have one"
        echo "Not checked against its attestation: $version has none"
    else
        gh attestation verify "$work/$name.tar.gz" --repo "$repo" >/dev/null 2>&1 \
            || fail "the download is not attested by the release workflow of $repo: it may not be what was released"
        echo "Checked against its attestation, by the release workflow of $repo"
    fi
fi

tar -xzf "$work/$name.tar.gz" -C "$work"
mkdir -p "$target"
mv "$work/$name/qoqi" "$target/qoqi"
chmod +x "$target/qoqi"

echo "Installed $("$target/qoqi" version) in $target"
case ":$PATH:" in
    *":$target:"*) ;;
    *) echo "Add it to your PATH: export PATH=\"$target:\$PATH\"" ;;
esac
