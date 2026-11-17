import numpy as np
from scipy.io import wavfile


AUDIO="/home/tatianno/Downloads/EGuitarFSBS-clean bridge small SFZ+WAV-20260807/samples/bridge/small/A2_s2_01.wav"


def aplicar_chorus(
    audio, fs, rate=1.5, depth_ms=3.0, delay_base_ms=20.0, mix=0.5
):
  """Aplica o efeito Chorus em um array mono float [-1.0, 1.0].

  - rate: Frequência do LFO em Hz (velocidade da oscilação, ex: 0.5 a 3 Hz)
  - depth_ms: Profundidade da modulação em milissegundos (ex: 1 a 5 ms)
  - delay_base_ms: Atraso fixo central em milissegundos (ex: 15 a 30 ms)
  - mix: Balanço entre sinal seco (0.0) e com efeito (1.0)
  """
  n_amostras = len(audio)
  indices = np.arange(n_amostras, dtype=np.float64)

  # 1. Cria o LFO (onda senoidal lenta oscilando entre -1 e +1)
  tempo = indices / fs
  lfo = np.sin(2.0 * np.pi * rate * tempo)

  # 2. Calcula o atraso variável em número de amostras
  delay_base_amostras = (delay_base_ms / 1000.0) * fs
  depth_amostras = (depth_ms / 1000.0) * fs
  atraso_variavel = delay_base_amostras + (depth_amostras * lfo)

  # 3. Calcula as posições de leitura no passado (índices fracionários)
  indices_leitura = indices - atraso_variavel

  # 4. Interpola o sinal nos índices fracionários (preenche com 0 antes do início)
  sinal_atrasado = np.interp(
      indices_leitura, indices, audio, left=0.0, right=0.0
  )

  # 5. Mistura o sinal original (Dry) com o sinal modulado (Wet)
  saida = (1.0 - mix) * audio + mix * sinal_atrasado
  return np.clip(saida, -1.0, 1.0)


# --- EXEMPLO DE USO ---
if __name__ == "__main__":
  fs, data = wavfile.read(AUDIO)
  if data.ndim > 1:
    data = data[:, 0]

  audio_float = data.astype(np.float64) / 32768.0

  # Aplica um chorus clássico de guitarra/sintetizador
  audio_chorus = aplicar_chorus(
      audio_float, fs, rate=1.2, depth_ms=2, delay_base_ms=22.0, mix=1
  )

  wavfile.write("audio_chorus.wav", fs, (audio_chorus * 32767).astype(np.int16))