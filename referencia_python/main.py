from pathlib import Path

import numpy as np
from scipy.io import wavfile
from scipy.signal import butter, fftconvolve, resample_poly, sosfilt


def carregar_wav_mono(caminho):
  fs, data = wavfile.read(caminho)

  # Normaliza ANTES de fazer a média dos canais: np.mean converte int16 -> float64,
  # e aí o teste de dtype inteiro falhava, deixando o áudio na escala ±32768.
  if np.issubdtype(data.dtype, np.integer):
    info = np.iinfo(data.dtype)
    max_val = max(abs(info.min), info.max)
    audio = data.astype(np.float64) / float(max_val)
  else:
    audio = data.astype(np.float64)

  if audio.ndim > 1:
    audio = np.mean(audio, axis=1)

  return fs, audio


def salvar_wav_16bit(caminho, fs, audio):
  # Fade-out de 50ms no final absoluto do arquivo
  fade_amostras = min(len(audio), int(0.050 * fs))
  audio[-fade_amostras:] *= np.linspace(1.0, 0.0, fade_amostras)

  pico = np.max(np.abs(audio))
  if pico > 0:
    audio = (audio / pico) * 0.891
  audio_int16 = np.clip(audio * 32767.0, -32768, 32767).astype(np.int16)
  wavfile.write(caminho, fs, audio_int16)


def aplicar_noise_gate_rigido(audio, fs, threshold_db=-42.0, release_ms=100.0):
  """Noise gate com atenuação real (-90 dB de redução) e zeragem absoluta abaixo do piso."""
  threshold_linear = 10.0 ** (threshold_db / 20.0)

  # Envelope RMS suavizado
  sos_env = butter(2, 30.0 / (0.5 * fs), btype="low", output="sos")
  envelope = np.sqrt(np.maximum(0.0, sosfilt(sos_env, audio**2)))

  # Curva de ganho: 1.0 acima do limiar, cai exponencialmente (razão 1:4) abaixo dele
  ganho_alvo = np.ones_like(envelope)
  abaixo = envelope < threshold_linear
  ganho_alvo[abaixo] = (
      envelope[abaixo] / (threshold_linear + 1e-12)
  ) ** 3

  # Suaviza apenas o fechamento (release)
  freq_rel = 1000.0 / (2.0 * np.pi * max(5.0, release_ms))
  sos_rel = butter(
      1, min(freq_rel, 0.45 * fs) / (0.5 * fs), btype="low", output="sos"
  )
  ganho_suave = np.clip(sosfilt(sos_rel, ganho_alvo), 0.0, 1.0)

  # Onde o ganho caiu abaixo de 0.001 (-60 dB), força zero exato para não sobrar resíduo
  ganho_suave[ganho_suave < 0.001] = 0.0

  return audio * ganho_suave


def preparar_impulse_response(caminho_ir, fs_alvo, duracao_max_ms=150.0):
  fs_ir, ir = carregar_wav_mono(caminho_ir)

  pico = np.max(np.abs(ir))
  indices_ativos = np.where(np.abs(ir) >= 0.05 * pico)[0]
  if len(indices_ativos) > 0:
    inicio = max(0, indices_ativos[0] - 2)
    ir = ir[inicio:]

  if fs_ir != fs_alvo:
    ir = resample_poly(ir, fs_alvo, fs_ir)

  max_amostras = int((duracao_max_ms / 1000.0) * fs_alvo)
  if len(ir) > max_amostras:
    ir = ir[:max_amostras]

  # Fade-out longo nos últimos 25ms da IR garante decaimento limpo até zero
  fade_len = min(len(ir), int(0.025 * fs_alvo))
  ir[-fade_len:] *= np.linspace(1.0, 0.0, fade_len) ** 2

  energia = np.sqrt(np.sum(ir**2))
  if energia > 0:
    ir = ir / energia

  return ir


def saturar_com_oversampling(audio, fs, drive=0.6):
  fator = 4
  fs_over = fs * fator
  n_original = len(audio)

  # 1. PADDING DE ZEROS (200ms) antes do resampling para eliminar 100% do ruído de borda
  pad_amostras = int(0.2 * fs)
  audio_padded = np.pad(audio, (pad_amostras, pad_amostras), mode="constant")

  sinal_over = resample_poly(audio_padded, fator, 1)

  # Envelope do sinal sobreamostrado
  sos_env = butter(1, 20.0 / (0.5 * fs_over), btype="low", output="sos")
  env = sosfilt(sos_env, np.abs(sinal_over))

  ganho = 2.0 + (drive**2) * 18.0
  # Bias proporcional ao envelope: quando o áudio vai a zero, o bias também vai a zero!
  # Isso impede que o circuito gere ruído ou DC offset no silêncio.
  bias = 0.25 * env
  sat = np.tanh((sinal_over * ganho) + bias) - np.tanh(bias)
  sat /= 1.0 + 0.35 * np.log1p(ganho)

  sinal_down = resample_poly(sat, 1, fator)

  # Remove o padding mantendo exatamente o tamanho original sem artefato de borda
  sinal_recortado = sinal_down[pad_amostras : pad_amostras + n_original]

  # DC Blocker suave
  sos_dc = butter(2, 30.0 / (0.5 * fs), btype="high", output="sos")
  return sosfilt(sos_dc, sinal_recortado)


def aplicar_gabinete_ir(
    audio, fs, caminho_ir, corte_graves_hz=85.0, corte_agudos_hz=6200.0
):
  ir = preparar_impulse_response(caminho_ir, fs)

  # Convolução sem cortar abruptamente: mantém o tamanho original mas faz fade-out suave no fim
  convoluido = fftconvolve(audio, ir, mode="full")[: len(audio)]

  nyquist = 0.5 * fs
  if corte_graves_hz:
    sos_hp = butter(2, corte_graves_hz / nyquist, btype="high", output="sos")
    convoluido = sosfilt(sos_hp, convoluido)

  if corte_agudos_hz:
    sos_lp = butter(3, corte_agudos_hz / nyquist, btype="low", output="sos")
    convoluido = sosfilt(sos_lp, convoluido)

  return convoluido


# ==============================================================================
# EXECUÇÃO BLINDADA CONTRA RUÍDO
# ==============================================================================
if __name__ == "__main__":
  base = Path(__file__).resolve().parent
  arquivo_guitarra = base.parent / "testdata" / "guitarra_di.wav"
  arquivo_ir = base.parent / "testdata" / "gabinete_2x12_sm57.wav"
  arquivo_saida = base / "saida" / "guitarra_limpa_final.wav"

  fs, guitarra_di = carregar_wav_mono(arquivo_guitarra)

  # 0. Fade curto nas bordas da DI: o arquivo termina com um clique (corte da gravação)
  #    que, sem isso, entraria no amp e viraria um estalo distorcido no fim.
  fade_in = int(0.005 * fs)
  fade_out = int(0.030 * fs)
  guitarra_di[:fade_in] *= np.linspace(0.0, 1.0, fade_in)
  guitarra_di[-fade_out:] *= np.linspace(1.0, 0.0, fade_out) ** 2

  # 1. Gate de entrada: corta ruído da interface antes do ganho
  sinal = aplicar_noise_gate_rigido(
      guitarra_di, fs, threshold_db=-40.0, release_ms=100.0
  )

  # 2. Saturação com padding anti-borda e bias auto-zerável
  sinal_amp = saturar_com_oversampling(sinal, fs, drive=0.65)

  # 3. Gate pós-amp: mata qualquer resíduo harmônico gerado pela distorção no fim das notas
  sinal_amp_limpo = aplicar_noise_gate_rigido(
      sinal_amp, fs, threshold_db=-38.0, release_ms=120.0
  )

  # 4. Gabinete IR
  resultado_final = aplicar_gabinete_ir(
      sinal_amp_limpo,
      fs,
      caminho_ir=arquivo_ir,
      corte_graves_hz=85.0,
      corte_agudos_hz=6200.0,
  )

  # 5. Salva com fade-out final
  arquivo_saida.parent.mkdir(exist_ok=True)
  salvar_wav_16bit(arquivo_saida, fs, resultado_final)
  print(f"Áudio salvo sem ruído no fim: '{arquivo_saida}'")