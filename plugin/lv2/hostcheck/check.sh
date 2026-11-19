#!/bin/sh
# Confere o bundle LV2 (dist/lv2/ampsim.lv2) num host de verdade (lilv), sem DAW: a saída do
# plugin com um preset de fábrica tem de bater com o render offline da app (hostcheck/ref), e o
# estado salvo e restaurado tem de dar o mesmo som; o mesmo para o plugin CLAP (clapcheck.c).
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
# preset estéreo: carrega, processa e restaura o estado com o mesmo som
echo "== Modulação (estéreo)"
"$tmp/hostcheck" "https://github.com/tatianno/ampsim#fabrica-modula%C3%A7%C3%A3o-estereo" "$rate" "$tmp/di.f32" "$tmp/plug.f32" || fail=1
# pedais, mono e estéreo: todos carregam, processam a DI e devolvem um sinal válido (os knobs no padrão)
for uri in $(grep -o 'https://github.com/tatianno/ampsim#pedal-[a-z0-9-]*' dist/lv2/ampsim.lv2/manifest.ttl); do
	out=$("$tmp/hostcheck" "$uri" "$rate" "$tmp/di.f32" "$tmp/plug.f32" 2>&1) || { echo "$out"; fail=1; continue; }
	venv/bin/python -I -c 'import sys, numpy as np
y = np.fromfile(sys.argv[1], dtype=np.float32)
ok = np.isfinite(y).all() and np.abs(y).max() > 1e-3
print("%-45s %s (pico %.2f)" % (sys.argv[2].split("#")[1], "ok" if ok else "FALHOU", np.abs(y).max()))
sys.exit(int(not ok))' "$tmp/plug.f32" "$uri" || fail=1
done
# CLAP: o mesmo .so; preset escolhido pelo nome, estéreo, latência fixa, troca ao vivo e estado
cc -O2 -Wall -Iplugin/lv2/clap/include -o "$tmp/clapcheck" plugin/lv2/hostcheck/clapcheck.c -ldl -lm
for name in Clean Metal Modulação; do
	echo "== CLAP: $name, $rate Hz"
	"$tmp/clapcheck" "$PWD/dist/lv2/ampsim.lv2/ampsim.so" "$name" "$rate" "$tmp/di.f32" "$tmp/plug.f32" | grep -v "^  parâmetro" || fail=1
	go run ./plugin/lv2/hostcheck/ref -estereo -fixa -preset "$name" "$rate" "$tmp/di.f32" "$tmp/ref.f32"
	venv/bin/python -I -c 'import sys, numpy as np
y = np.fromfile(sys.argv[1], dtype=np.float32).astype(np.float64)
r = np.fromfile(sys.argv[2], dtype=np.float32).astype(np.float64)
n = min(len(y), len(r)); d = np.abs(y[:n] - r[:n]).max()
print("CLAP (L) x render estéreo da app: diferença máxima %.3g (pico %.3g)" % (d, np.abs(r).max()))
sys.exit(int(d > 1e-5))' "$tmp/plug.f32" "$tmp/ref.f32" || fail=1
done
exit $fail
