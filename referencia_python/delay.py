import numpy as np
from scipy.io import wavfile


def aplicar_delay(
    audio,
    fs,
    tempo_ms=350.0,
    feedback=0.45,
    mix=0.35,
    filtro_agudos=0.3,
    cauda_segundos=2.0,
):
  """Aplica um efeito Delay com feedback e escurecimento progressivo (estilo Analog/Tape Delay).

  - tempo_ms: Tempo entre cada repetição em milissegundos (ex: 120ms =
  slapback, 350-500ms = eco rítmico)
  - feedback: Quanto do eco volta para repetir novamente (0.0 a 0.85)
  - mix: Balanço Dry/Wet (0.0 a 1.0)
  - filtro_agudos: Suavização das repetições (0.0 = delay digital limpo, 0.3 a
  0.7 = delay analógico quente)
  - cauda_segundos: Tempo extra no fim para as últimas repetições não cortarem
  """
  # Adiciona silêncio ao final para deixar os últimos ecos decaírem naturalmente
  amostras_extra = int(cauda_segundos * fs)
  audio_padded = np.pad(audio, (0, amostras_extra), mode="constant")
  n_amostras = len(audio_padded)

  saida = np.zeros(n_amostras, dtype=np.float64)

  # Tamanho do buffer circular corresponde exatamente ao tempo de atraso em amostras
  delay_amostras = max(1, int((tempo_ms / 1000.0) * fs))
  buffer = np.zeros(delay_amostras, dtype=np.float64)
  idx = 0
  estado_filtro = 0.0

  for i in range(n_amostras):
    # Lê a amostra atrasada que estava armazenada no buffer
    sinal_atrasado = buffer[idx]

    # Aplica um filtro passa-baixa de 1 polo no sinal de feedback (escurece cada repetição)
    estado_filtro = (sinal_atrasado * (1.0 - filtro_agudos)) + (
        estado_filtro * filtro_agudos
    )

    # Escreve no buffer: áudio atual + eco filtrado realimentado
    buffer[idx] = audio_padded[i] + (estado_filtro * feedback)

    # Avança o ponteiro circular
    idx = (idx + 1) % delay_amostras

    # Mistura o sinal seco (Dry) com o eco (Wet)
    saida[i] = (1.0 - mix) * audio_padded[i] + (mix * sinal_atrasado)

  return np.clip(saida, -1.0, 1.0)


# --- EXEMPLO DE USO ---
if __name__ == "__main__":
  AUDIO="/home/tatianno/Downloads/EGuitarFSBS-clean bridge small SFZ+WAV-20260807/samples/bridge/small/A2_s2_01.wav"

  fs, data = wavfile.read(AUDIO)
  if data.ndim > 1:
    data = data[:, 0]

  audio_float = data.astype(np.float64) / 32768.0

  # Delay estilo analógico de 380ms com 4-5 repetições suaves
  audio_delay = aplicar_delay(
      audio_float,
      fs,
      tempo_ms=380.0,
      feedback=0.45,
      mix=0.35,
      filtro_agudos=0.35,
      cauda_segundos=2.5,
  )

  wavfile.write("audio_delay.wav", fs, (audio_delay * 32767).astype(np.int16))