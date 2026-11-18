#!/bin/sh
# Instala o ampsim na pasta pessoal (sem sudo). Para outra pasta: PREFIX=/opt/ampsim ./install.sh
set -e
PREFIX="${PREFIX:-$HOME/.local}"
DIR="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$PREFIX/bin" "$PREFIX/share/applications"
install -m 755 "$DIR/ampsim" "$DIR/ampsim-cli" "$PREFIX/bin/"
for n in 48 64 128 256 512; do
	mkdir -p "$PREFIX/share/icons/hicolor/${n}x${n}/apps"
	install -m 644 "$DIR/icons/ampsim-$n.png" "$PREFIX/share/icons/hicolor/${n}x${n}/apps/ampsim.png"
done
# caminho completo no atalho: funciona mesmo se $PREFIX/bin não estiver no PATH
sed "s|^Exec=ampsim|Exec=$PREFIX/bin/ampsim|" "$DIR/ampsim.desktop" > "$PREFIX/share/applications/ampsim.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true
echo "ampsim instalado em $PREFIX (procure \"ampsim\" no menu de aplicativos)."
case ":$PATH:" in *":$PREFIX/bin:"*) ;; *) echo "dica: acrescente $PREFIX/bin ao PATH para usar ampsim e ampsim-cli no terminal." ;; esac
