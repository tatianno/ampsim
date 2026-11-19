#!/bin/sh
# Confere o bundle LV2 (dist/lv2/ampsim.lv2) num host de verdade (lilv), sem DAW: a saída do
# plugin com um preset de fábrica tem de bater com o render offline da app (hostcheck/ref), e o
# estado salvo e restaurado tem de dar o mesmo som.
# Uso: plugin/lv2/hostcheck/check.sh [DI.wav]   (rode plugin/lv2/build.sh antes)
set -eu
cd "$(dirname "$0")/../../.."
di=${1:-testdata/guitarra_di.wav}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cc -O2 -Wall -o "$tmp/hostcheck" plugin/lv2/hostcheck/hostcheck.c /usr/lib/x86_64-linux-gnu/liblilv-0.so.0 -lm
rate=$(venv/bin/python -I -c 'import sys, scipy.io.wavfile as w, numpy as np
sr, x = w.read(sys.argv[1])
x = x.astype(np.float64)
if x.ndim > 1: x = x.mean(axis=1)
if np.issubdtype(w.read(sys.argv[1])[1].dtype, np.integer): x /= 32768.0
x.astype(np.float32).tofile(sys.argv[2]); print(sr)' "$di" "$tmp/di.f32")
export LV2_PATH="$PWD/dist/lv2"
fail=0
for p in "clean Clean" "metal Metal" "modula%C3%A7%C3%A3o Modulação"; do
	set -- $p
	echo "== $2, $rate Hz"
	"$tmp/hostcheck" "https://github.com/tatianno/ampsim#fabrica-$1" "$rate" "$tmp/di.f32" "$tmp/plug.f32" || fail=1
	go run ./plugin/lv2/hostcheck/ref -preset "$2" "$rate" "$tmp/di.f32" "$tmp/ref.f32"
	venv/bin/python -I -c 'import sys, numpy as np
y = np.fromfile(sys.argv[1], dtype=np.float32).astype(np.float64)
r = np.fromfile(sys.argv[2], dtype=np.float32).astype(np.float64)
n = min(len(y), len(r)); d = np.abs(y[:n] - r[:n]).max()
print("plugin x render offline da app: diferença máxima %.3g (pico %.3g)" % (d, np.abs(r).max()))
sys.exit(int(d > 1e-5))' "$tmp/plug.f32" "$tmp/ref.f32" || fail=1
done
exit $fail
