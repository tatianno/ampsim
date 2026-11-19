#!/bin/sh
# Instala o AmpSim na pasta pessoal (sem sudo). Para outra pasta: PREFIX=/opt/ampsim ./install.sh
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
# plugin LV2: a pasta do usuário que os hosts (Audacity, Ardour, Carla) leem
if [ -d "$DIR/ampsim.lv2" ]; then
	mkdir -p "$HOME/.lv2"
	rm -rf "$HOME/.lv2/ampsim.lv2"
	cp -r "$DIR/ampsim.lv2" "$HOME/.lv2/"
	mkdir -p "$HOME/.clap"
	ln -sfn "$HOME/.lv2/ampsim.lv2/ampsim.so" "$HOME/.clap/ampsim.clap" # o CLAP: o mesmo .so
	echo "plugins instalados: LV2 em $HOME/.lv2/ampsim.lv2, CLAP em $HOME/.clap/ampsim.clap"
fi
echo "AmpSim instalado em $PREFIX (procure \"AmpSim\" no menu de aplicativos)."
case ":$PATH:" in *":$PREFIX/bin:"*) ;; *) echo "dica: acrescente $PREFIX/bin ao PATH para usar ampsim e ampsim-cli no terminal." ;; esac
