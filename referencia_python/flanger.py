import numpy as np
from scipy.io import wavfile


def aplicar_flanger(
    audio, fs, rate=0.3, min_delay_ms=0.5, max_delay_ms=4.0, feedback=0.6, mix=0.5
):
  """Aplica o efeito Flanger com feedback em um array mono float [-1.0, 1.0].

  - rate: Frequência do LFO em Hz (bem lento, ex: 0.1 a 0.8 Hz)
  - min_delay_ms / max_delay_ms: Varredura do atraso em milissegundos (0.5 a 5
  ms)
  - feedback: Realimentação (-0.9 a +0.9). Valores negativos invertem a fase (som
  mais oco/vocal)
  - mix: Balanço Dry/Wet (0.5 dá o cancelamento de fase mais intenso)
  """
  n_amostras = len(audio)
  saida = np.zeros(n_amostras, dtype=np.float64)

  # 1. LFO variando suavemente entre min_delay_ms e max_delay_ms
  tempo = np.arange(n_amostras, dtype=np.float64) / fs
  lfo = 0.5 * (1.0 + np.sin(2.0 * np.pi * rate * tempo))  # Oscila entre 0 e 1
  atrasos_ms = min_delay_ms + (max_delay_ms - min_delay_ms) * lfo
  atrasos_amostras = (atrasos_ms / 1000.0) * fs

  # 2. Buffer circular (Ring Buffer) para armazenar o histórico recente com feedback
  max_buf = int(np.ceil((max_delay_ms / 1000.0) * fs)) + 2
  buffer = np.zeros(max_buf, dtype=np.float64)
  idx_escrita = 0

  # 3. Processamento amostra por amostra
  for i in range(n_amostras):
    atraso = atrasos_amostras[i]

    # Calcula posição fracionária de leitura no passado dentro do buffer circular
    pos_leitura = (idx_escrita - atraso) % max_buf
    idx0 = int(pos_leitura)
    idx1 = (idx0 + 1) % max_buf
    frac = pos_leitura - idx0

    # Interpolação linear entre duas amostras adjacentes
    sinal_atrasado = buffer[idx0] * (1.0 - frac) + buffer[idx1] * frac

    # Escreve no buffer a entrada atual + o feedback da saída atrasada
    buffer[idx_escrita] = audio[i] + (feedback * sinal_atrasado)
    idx_escrita = (idx_escrita + 1) % max_buf

    # Soma sinal seco (Dry) + sinal atrasado (Wet)
    saida[i] = (1.0 - mix) * audio[i] + (mix * sinal_atrasado)

  return np.clip(saida, -1.0, 1.0)


# --- EXEMPLO DE USO ---
if __name__ == "__main__":
  AUDIO="/home/tatianno/Downloads/EGuitarFSBS-clean bridge small SFZ+WAV-20260807/samples/bridge/small/A2_s2_01.wav"

  fs, data = wavfile.read(AUDIO)
  if data.ndim > 1:
    data = data[:, 0]

  audio_float = data.astype(np.float64) / 32768.0

  # Flanger clássico estilo "jet plane"
  audio_flanger = aplicar_flanger(
      audio_float,
      fs,
      rate=0.25,
      min_delay_ms=0.5,
      max_delay_ms=3.5,
      feedback=0.7,
      mix=0.5,
  )

  wavfile.write(
      "audio_flanger.wav", fs, (audio_flanger * 32767).astype(np.int16)
  )