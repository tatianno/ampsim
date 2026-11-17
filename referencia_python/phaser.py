import numpy as np
from scipy.io import wavfile


def aplicar_phaser(
    audio,
    fs,
    rate=0.5,
    min_freq=200.0,
    max_freq=1800.0,
    estagios=4,
    feedback=0.5,
    mix=0.5,
):
  """Aplica um efeito Phaser de N estágios em um array mono float [-1.0, 1.0].

  - rate: Velocidade do LFO em Hz (ex: 0.2 a 2.0 Hz)
  - min_freq / max_freq: Faixa de varredura dos vales de fase em Hz
  - estagios: Número de filtros All-Pass em série (4 = Phase 90, 6 ou 8 = mais
  intenso)
  - feedback: Realimentação do sinal filtrado (-0.9 a 0.9) para acentuar a
  ressonância
  - mix: Balanço Dry/Wet (0.5 dá o cancelamento máximo de fase)
  """
  n_amostras = len(audio)
  saida = np.zeros(n_amostras, dtype=np.float64)

  # 1. Gera o LFO variando suavemente entre min_freq e max_freq (escala logarítmica soa mais musical)
  tempo = np.arange(n_amostras, dtype=np.float64) / fs
  lfo = 0.5 * (1.0 + np.sin(2.0 * np.pi * rate * tempo))  # Oscila entre 0 e 1
  freqs_corte = min_freq * ((max_freq / min_freq) ** lfo)

  # 2. Pré-calcula o coeficiente 'a' do filtro All-Pass de 1ª ordem para cada amostra
  # Fórmula bilinear: a = (tan(pi * fc / fs) - 1) / (tan(pi * fc / fs) + 1)
  tan_w = np.tan(np.pi * freqs_corte / fs)
  coef_a = (tan_w - 1.0) / (tan_w + 1.0)

  # Memória de estado dos filtros All-Pass (x[n-1] e y[n-1] de cada estágio)
  x_prev = np.zeros(estagios, dtype=np.float64)
  y_prev = np.zeros(estagios, dtype=np.float64)
  ultimo_y = 0.0

  # 3. Processamento amostra por amostra
  for i in range(n_amostras):
    a = coef_a[i]

    # Entrada do primeiro estágio recebe o áudio + feedback da saída anterior
    sinal = audio[i] + (feedback * ultimo_y)

    # Passa o sinal em cascata pelos N filtros All-Pass
    for s in range(estagios):
      # Equação de diferença do filtro All-Pass de 1ª ordem:
      # y[n] = a * x[n] + x[n-1] - a * y[n-1]
      y = (a * sinal) + x_prev[s] - (a * y_prev[s])
      x_prev[s] = sinal
      y_prev[s] = y
      sinal = y

    ultimo_y = sinal

    # Mistura sinal seco + sinal com fase deslocada
    saida[i] = (1.0 - mix) * audio[i] + (mix * sinal)

  return np.clip(saida, -1.0, 1.0)


# --- EXEMPLO DE USO ---
if __name__ == "__main__":
  AUDIO="/home/tatianno/Downloads/EGuitarFSBS-clean bridge small SFZ+WAV-20260807/samples/bridge/small/A2_s2_01.wav"

  fs, data = wavfile.read(AUDIO)
  if data.ndim > 1:
    data = data[:, 0]

  audio_float = data.astype(np.float64) / 32768.0

  # Phaser clássico estilo guitarra funk/psicodélica
  audio_phaser = aplicar_phaser(
      audio_float,
      fs,
      rate=0.6,
      min_freq=300.0,
      max_freq=2000.0,
      estagios=4,
      feedback=0.6,
      mix=0.5,
  )

  wavfile.write("audio_phaser.wav", fs, (audio_phaser * 32767).astype(np.int16))