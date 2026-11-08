#!/usr/bin/env bash
# Package the Linux build produced by `mygo build -platform linux/amd64`.
#
# mygo already lays the application out next to the binary, but its own
# archives carry the Windows engine and payload along, and they never put the
# launcher on PATH. This script trims the tree to the Linux payload and builds
# both distribution formats from it:
#
#   build/linux-amd64/ferry_<version>_amd64.deb
#   build/linux-amd64/ferry-<version>-linux-amd64.tar.gz
#
# Run it from the repository root on Linux (CI or a local Ubuntu box):
#   VERSION=1.1.0 bash scripts/package-linux.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
app="$root/build/linux-amd64"

if [[ ! -x "$app/ferry" ]]; then
  echo "build/linux-amd64/ferry is missing; run 'npx --no-install mygo build -platform linux/amd64' first" >&2
  exit 1
fi

version="${VERSION:-$(sed -n 's/^ *version: *"\([^"]*\)".*/\1/p' "$root/mygo.config.ts" | head -n1)}"
if [[ -z "$version" ]]; then
  echo "could not determine the application version; set VERSION" >&2
  exit 1
fi

# Windows-only payload that mygo copies along with the shared resources.
rm -rf "$app/engine/win" "$app/engine-setup.exe" "$app/engine-setup.exe.manifest"
rm -f "$app"/*.deb "$app"/*.tar.gz

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# ── .deb ────────────────────────────────────────────────────────────────
# /opt/ferry keeps the binary next to engine/ and licenses/, which is exactly
# where the application looks for its engine bundle at runtime.
pkg="$work/deb"
mkdir -p "$pkg/opt" "$pkg/usr/bin" "$pkg/usr/share/applications" \
  "$pkg/usr/share/icons/hicolor/256x256/apps" \
  "$pkg/usr/share/icons/hicolor/512x512/apps" "$pkg/DEBIAN"
cp -a "$app" "$pkg/opt/ferry"
find "$pkg/opt/ferry" -maxdepth 1 -type f \( -name '*.deb' -o -name '*.tar.gz' \) -delete
ln -s /opt/ferry/ferry "$pkg/usr/bin/ferry"
cp "$app/ferry-256x256.png" "$pkg/usr/share/icons/hicolor/256x256/apps/ferry.png"
cp "$app/ferry-512x512.png" "$pkg/usr/share/icons/hicolor/512x512/apps/ferry.png"
cat >"$pkg/usr/share/applications/ferry.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Name=Ferry
Comment=Route traffic through proxies with ordered per-process rules
Exec=/opt/ferry/ferry
Icon=ferry
Categories=Network;Utility;
Keywords=proxy;socks5;http;rule;
Terminal=false
StartupWMClass=ferry
EOF

installed_kb="$(du -sk "$pkg/opt/ferry" | cut -f1)"
cat >"$pkg/DEBIAN/control" <<EOF
Package: ferry
Version: $version
Architecture: amd64
Maintainer: Ferry <https://github.com/ayuayue/ferry>
Installed-Size: $installed_kb
Depends: libgtk-3-0 | libgtk-3-0t64, libwebkit2gtk-4.1-0
Recommends: policykit-1, fonts-noto-cjk
Section: net
Priority: optional
Homepage: https://github.com/ayuayue/ferry
Description: Desktop proxy router built on ProxyBridge
 Ferry applies ordered rules to traffic per process and forwards it through
 HTTP or SOCKS5 proxies. The engine intercepts at the kernel level and runs as
 a systemd service, so the window itself does not need administrator rights.
EOF
# The engine service is installed from inside the application; removing the
# package must not leave a unit behind that points at a deleted binary.
cat >"$pkg/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = "remove" ] || [ "$1" = "purge" ]; then
  if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now ferry-engine.service >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/ferry-engine.service
    systemctl daemon-reload >/dev/null 2>&1 || true
  fi
  # The engine bundle is Ferry's own copy; /etc/proxybridge holds profiles the
  # user authored, so it is left alone.
  rm -rf /usr/lib/ferry
fi
exit 0
EOF
chmod 0755 "$pkg/DEBIAN/postrm"

dpkg-deb --build --root-owner-group "$pkg" "$app/ferry_${version}_amd64.deb" >/dev/null

# ── portable archive ────────────────────────────────────────────────────
portable="$work/portable"
mkdir -p "$portable"
cp -a "$app" "$portable/ferry"
find "$portable/ferry" -maxdepth 1 -type f \( -name '*.deb' -o -name '*.tar.gz' \) -delete
tar -czf "$app/ferry-${version}-linux-amd64.tar.gz" -C "$portable" ferry

echo "packaged ferry $version"
ls -l "$app/ferry_${version}_amd64.deb" "$app/ferry-${version}-linux-amd64.tar.gz"
