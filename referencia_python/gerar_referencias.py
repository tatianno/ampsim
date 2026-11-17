"""Gera as saídas de referência (WAV float32) usadas pelos testes Go.

Uso (a partir da raiz do projeto):
  venv/bin/python referencia_python/gerar_referencias.py

Os parâmetros aqui precisam ser os mesmos usados nos testes em Go
(dsp/referencia_test.go).
"""

from pathlib import Path

import numpy as np
from scipy.io import wavfile
from scipy.signal import butter, sosfilt

from chorus import aplicar_chorus
from delay import aplicar_delay
from flanger import aplicar_flanger
import main as amp
from phaser import aplicar_phaser
from reverb import aplicar_reverb

BASE = Path(__file__).resolve().parent
TESTDATA = BASE.parent / "testdata"
SAIDA = TESTDATA / "referencia"


def salvar(nome, fs, audio):
  wavfile.write(SAIDA / nome, fs, np.asarray(audio, dtype=np.float32))
  print(f"  {nome:22s} {len(audio):7d} amostras  pico {np.max(np.abs(audio)):.3f}")


def main():
  SAIDA.mkdir(exist_ok=True)

  fs, di = amp.carregar_wav_mono(TESTDATA / "guitarra_di.wav")
  fade_in = int(0.005 * fs)
  fade_out = int(0.030 * fs)
  di[:fade_in] *= np.linspace(0.0, 1.0, fade_in)
  di[-fade_out:] *= np.linspace(1.0, 0.0, fade_out) ** 2
  print(f"fs = {fs}")
  salvar("di.wav", fs, di)

  # Filtros lineares (Butterworth) usados no gabinete
  nyq = 0.5 * fs
  filtros = sosfilt(butter(2, 85.0 / nyq, btype="high", output="sos"), di)
  filtros = sosfilt(butter(3, 6200.0 / nyq, btype="low", output="sos"), filtros)
  salvar("filtros.wav", fs, filtros)

  # Cadeia do amp (igual ao main.py)
  gate1 = amp.aplicar_noise_gate_rigido(di, fs, threshold_db=-40.0, release_ms=100.0)
  salvar("gate1.wav", fs, gate1)
  sat = amp.saturar_com_oversampling(gate1, fs, drive=0.65)
  salvar("sat.wav", fs, sat)
  gate2 = amp.aplicar_noise_gate_rigido(sat, fs, threshold_db=-38.0, release_ms=120.0)
  salvar("gate2.wav", fs, gate2)
  ir = amp.preparar_impulse_response(TESTDATA / "gabinete_2x12_sm57.wav", fs)
  salvar("ir_preparada.wav", fs, ir)
  cab = amp.aplicar_gabinete_ir(gate2, fs, TESTDATA / "gabinete_2x12_sm57.wav")
  salvar("cab.wav", fs, cab)

  # Efeitos (entrada com metade do nível para não chegar no clip de ±1)
  x = 0.5 * di
  salvar("chorus.wav", fs, aplicar_chorus(x, fs, rate=1.2, depth_ms=2.0, delay_base_ms=22.0, mix=0.5))
  salvar("flanger.wav", fs, aplicar_flanger(x, fs, rate=0.3, min_delay_ms=0.5, max_delay_ms=4.0, feedback=0.6, mix=0.5))
  salvar("phaser.wav", fs, aplicar_phaser(x, fs, rate=0.5, min_freq=200.0, max_freq=1800.0, estagios=4, feedback=0.5, mix=0.5))
  salvar("delay.wav", fs, aplicar_delay(x, fs, tempo_ms=380.0, feedback=0.45, mix=0.35, filtro_agudos=0.35, cauda_segundos=2.0))
  salvar("reverb.wav", fs, aplicar_reverb(x, fs, tamanho_sala=0.8, amortecimento=0.4, mix=0.35, cauda_segundos=1.5))


if __name__ == "__main__":
  main()
