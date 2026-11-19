#!/bin/sh
# Compila o bundle LV2 do AmpSim: dist/lv2/ampsim.lv2/{ampsim.so,manifest.ttl,ampsim.ttl}, com
# os presets de fábrica. Uso: plugin/lv2/build.sh [--install]
#   --install copia para ~/.lv2/ampsim.lv2, já com os presets do usuário (o app mantém a
#   lista em dia dali em diante).
set -eu
cd "$(dirname "$0")/../.."
out=dist/lv2/ampsim.lv2
rm -rf "$out"
mkdir -p "$out"
export GOEXPERIMENT=simd # núcleos SIMD do NAM (veja nam/kernels_simd_amd64.go)
go build -buildmode=c-shared -trimpath -ldflags "-s -w -extldflags=-Wl,-z,nodelete" -o "$out/ampsim.so" ./plugin/lv2
rm -f "$out/ampsim.h"
go run ./plugin/lv2/ttlgen -out "$out"
echo "bundle: $out"
if [ "${1:-}" = "--install" ]; then
	dest="$HOME/.lv2/ampsim.lv2"
	mkdir -p "$HOME/.lv2"
	rm -rf "$dest"
	cp -r "$out" "$dest"
	go run ./plugin/lv2/ttlgen -usuario -out "$dest"
	echo "instalado em $dest"
fi
