import numpy as np
from scipy.io import wavfile


def _comb_filter(audio, delay_amostras, feedback, damping):
  """Filtro Comb com amortecimento (Low-Pass) no feedback para simular absorção das paredes."""
  n = len(audio)
  saida = np.zeros(n, dtype=np.float64)
  buffer = np.zeros(delay_amostras, dtype=np.float64)
  idx = 0
  filtro_store = 0.0

  for i in range(n):
    y = buffer[idx]
    # Filtro passa-baixa de 1 polo: agudos decaem mais rápido que os graves
    filtro_store = (y * (1.0 - damping)) + (filtro_store * damping)
    buffer[idx] = audio[i] + (filtro_store * feedback)

    saida[i] = y
    idx = (idx + 1) % delay_amostras

  return saida


def _allpass_filter(audio, delay_amostras, ganho=0.5):
  """Filtro All-Pass com delay para aumentar a densidade de reflexões sem alterar o timbre."""
  n = len(audio)
  saida = np.zeros(n, dtype=np.float64)
  buffer = np.zeros(delay_amostras, dtype=np.float64)
  idx = 0

  for i in range(n):
    buf_out = buffer[idx]
    entrada = audio[i]
    saida[i] = -entrada + buf_out
    buffer[idx] = entrada + (buf_out * ganho)
    idx = (idx + 1) % delay_amostras

  return saida


def aplicar_reverb(
    audio, fs, tamanho_sala=0.8, amortecimento=0.4, mix=0.35, cauda_segundos=1.5
):
  """Aplica um Reverb Algorítmico (Schroeder/Freeverb) em um array mono float [-1.0, 1.0].

  - tamanho_sala: Feedback dos ecos (0.5 = sala pequena, 0.85 = salão grande,
  0.95 = catedral)
  - amortecimento: Absorção de agudos nas reflexões (0.0 = brilhante/azulejo,
  0.8 = escuro/carpete)
  - mix: Balanço Dry/Wet (0.0 a 1.0)
  - cauda_segundos: Silêncio extra adicionado no fim para o reverb não cortar
  abruptamente
  """
  # Adiciona silêncio no final para a cauda do reverb morrer naturalmente
  amostras_extra = int(cauda_segundos * fs)
  audio_padded = np.pad(audio, (0, amostras_extra), mode="constant")

  # Tempos de delay em ms escolhidos com números primos para evitar padrões metálicos
  delays_comb_ms = [29.7, 37.1, 41.1, 43.7]
  delays_allpass_ms = [5.0, 1.7]

  # 1. Soma 4 filtros Comb em paralelo (cria os ecos principais)
  soma_combs = np.zeros_like(audio_padded)
  for d_ms in delays_comb_ms:
    d_amostras = max(1, int((d_ms / 1000.0) * fs))
    soma_combs += _comb_filter(
        audio_padded, d_amostras, feedback=tamanho_sala, damping=amortecimento
    )

  soma_combs *= 0.25  # Normaliza a soma das 4 linhas

  # 2. Passa por 2 filtros All-Pass em série (difunde os ecos em uma cauda contínua)
  sinal_difuso = soma_combs
  for d_ms in delays_allpass_ms:
    d_amostras = max(1, int((d_ms / 1000.0) * fs))
    sinal_difuso = _allpass_filter(sinal_difuso, d_amostras, ganho=0.5)

  # 3. Mistura o sinal seco (Dry) com o reverberado (Wet)
  saida = (1.0 - mix) * audio_padded + (mix * sinal_difuso)
  return np.clip(saida, -1.0, 1.0)


# --- EXEMPLO DE USO ---
if __name__ == "__main__":
  AUDIO="/home/tatianno/Downloads/EGuitarFSBS-clean bridge small SFZ+WAV-20260807/samples/bridge/small/A2_s2_01.wav"

  fs, data = wavfile.read(AUDIO)
  if data.ndim > 1:
    data = data[:, 0]

  audio_float = data.astype(np.float64) / 32768.0

  # Simula um hall médio com paredes levemente absorventes
  audio_reverb = aplicar_reverb(
      audio_float,
      fs,
      tamanho_sala=0.84,
      amortecimento=0.45,
      mix=0.35,
      cauda_segundos=2.0,
  )

  wavfile.write("audio_reverb.wav", fs, (audio_reverb * 32767).astype(np.int16))