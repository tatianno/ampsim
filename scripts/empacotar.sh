#!/bin/sh
# Gera os pacotes do AmpSim para Linux em dist/:
#   ampsim_VERSAO_amd64.deb          (sudo apt install ./ampsim_VERSAO_amd64.deb)
#   ampsim-VERSAO-linux-amd64.tar.gz (./install.sh, sem sudo)
# Os dois levam o plugin LV2 (Audacity, Ardour, Carla...): o .tar.gz instala o bundle em ~/.lv2; o
# .deb instala o .so em /usr/lib/ampsim e o app monta o bundle em ~/.lv2 ao abrir (um plugin por
# preset, com os do usuário).
#
# Uso: scripts/empacotar.sh 0.3.0
set -e
umask 022 # permissões padrão de pacote (pastas 755, arquivos 644)
VERSAO="${1:?uso: scripts/empacotar.sh VERSAO (ex.: 0.3.0)}"
ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
RAIZ="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$RAIZ/dist"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cd "$RAIZ"
mkdir -p "$DIST"

echo "== compilando $VERSAO ($ARCH)"
LDFLAGS="-s -w -X main.version=$VERSAO"
# SIMD experimental do Go (AVX2 + FMA no WaveNet do NAM, ~3× menos CPU no modelo completo);
# em CPUs sem AVX2/FMA o binário usa o caminho escalar sozinho
export GOEXPERIMENT="${GOEXPERIMENT:-simd}"
CGO_ENABLED=1 go build -trimpath -ldflags "$LDFLAGS" -o "$TMP/ampsim" ./cmd/ampapp
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o "$TMP/ampsim-cli" ./cmd/ampcli

plugin/lv2/build.sh >/dev/null # dist/lv2/ampsim.lv2
LV2="$DIST/lv2/ampsim.lv2"

"$TMP/ampsim" -versao | grep -q 'AVX2' || echo "aviso: binário sem os núcleos SIMD (GOEXPERIMENT=$GOEXPERIMENT; CPU sem AVX2/FMA?)"

echo "== tar.gz"
T="$TMP/ampsim-$VERSAO-linux-$ARCH"
mkdir -p "$T"
cp "$TMP/ampsim" "$TMP/ampsim-cli" packaging/linux/ampsim.desktop \
   packaging/linux/install.sh packaging/linux/uninstall.sh LICENSE README.md "$T/"
cp -r packaging/linux/icons "$LV2" "$T/"
chmod 755 "$T/install.sh" "$T/uninstall.sh"
tar -C "$TMP" -czf "$DIST/ampsim-$VERSAO-linux-$ARCH.tar.gz" "ampsim-$VERSAO-linux-$ARCH"

echo "== deb"
D="$TMP/deb"
mkdir -p "$D/DEBIAN" "$D/usr/bin" "$D/usr/share/applications" "$D/usr/share/doc/ampsim" "$D/usr/lib/ampsim"
install -m 644 "$LV2/ampsim.so" "$D/usr/lib/ampsim/"
install -m 755 packaging/linux/postrm "$D/DEBIAN/"
install -m 755 "$TMP/ampsim" "$TMP/ampsim-cli" "$D/usr/bin/"
install -m 644 packaging/linux/ampsim.desktop "$D/usr/share/applications/"
for n in 48 64 128 256 512; do
	mkdir -p "$D/usr/share/icons/hicolor/${n}x${n}/apps"
	install -m 644 "packaging/linux/icons/ampsim-$n.png" "$D/usr/share/icons/hicolor/${n}x${n}/apps/ampsim.png"
done
install -m 644 LICENSE "$D/usr/share/doc/ampsim/copyright"
install -m 644 README.md "$D/usr/share/doc/ampsim/"
TAM="$(du -sk "$D/usr" | cut -f1)"
cat > "$D/DEBIAN/control" <<CONTROL
Package: ampsim
Version: $VERSAO
Section: sound
Priority: optional
Architecture: $ARCH
Installed-Size: $TAM
Maintainer: tatianno <tatianno@users.noreply.github.com>
Depends: libc6, libegl1, libgles2, libwayland-client0, libwayland-cursor0, libwayland-egl1, libx11-6, libx11-xcb1, libxcursor1, libxfixes3, libxkbcommon0, libxkbcommon-x11-0
Recommends: pipewire-jack, zenity, xdg-user-dirs
Homepage: https://github.com/tatianno/ampsim
Description: AmpSim, simulador de amplificador de guitarra e pedais
 Amp simulado ou modelos NAM (Neural Amp Modeler), gabinete por IR, pedais
 (gate, compressor, EQ paramétrico, chorus, flanger, phaser, delay, reverb) com
 loop de efeitos, afinador, tap tempo, presets e gravação, tocando ao vivo com
 baixa latência (JACK via PipeWire). Inclui o plugin LV2 que aplica os presets
 numa faixa (Audacity, Ardour, Carla): abra o app uma vez para ele aparecer.
CONTROL
dpkg-deb --root-owner-group --build "$D" "$DIST/ampsim_${VERSAO}_${ARCH}.deb" >/dev/null

echo "== pronto:"
ls -lh "$DIST"/ampsim*"$VERSAO"*
