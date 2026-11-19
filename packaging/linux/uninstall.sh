#!/bin/sh
# Remove o ampsim instalado pelo install.sh (os presets e gravações ficam).
set -e
PREFIX="${PREFIX:-$HOME/.local}"
rm -f "$PREFIX/bin/ampsim" "$PREFIX/bin/ampsim-cli" "$PREFIX/share/applications/ampsim.desktop"
for n in 48 64 128 256 512; do
	rm -f "$PREFIX/share/icons/hicolor/${n}x${n}/apps/ampsim.png"
done
rm -rf "$HOME/.lv2/ampsim.lv2"
command -v update-desktop-database >/dev/null && update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true
echo "ampsim removido de $PREFIX (presets em ~/.config/ampsim e gravações em Música/ampsim foram mantidos)."
