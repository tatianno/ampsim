# Simulador de Amplificador e Pedais em Go — Plano de Ação

> Aprovado em 17/11/2026. Este documento é a referência do projeto: decisões, arquitetura e lista de tarefas.
> Marque as caixas (`[x]`) conforme as tarefas forem concluídas.

## 1. Objetivo

Portar para Go os estudos de modulação e simulação de amplificador feitos em Python, chegando a:

1. **Aplicação standalone** com GUI: amp (Ganho, Grave, Médio, Agudo, Presença, Volume), gabinete por IR e pedais
   (Gate, Chorus, Flanger, Phaser, Delay, Reverb), tocando ao vivo pela guitarra ou a partir de um arquivo WAV.
2. **Plugin** que aplica os presets da app a uma faixa (gravada ou ao vivo), reaproveitando o mesmo núcleo de DSP:
   LV2 para o Audacity primeiro, depois CLAP para o Reaper.

## 2. Decisões tomadas

| Tema | Decisão | Motivo |
|---|---|---|
| Linguagem | Go (1.26 instalado) | Escolha do projeto |
| Ordem | Standalone primeiro, plugin depois | Mais fácil de depurar; o núcleo é reaproveitado |
| Áudio I/O | `gen2brain/malgo` (miniaudio) | Full-duplex, multiplataforma, C embutido |
| GUI | Gio (`gioui.org`) | Go puro, bom para desenhar knobs e medidores |
| Pedais na GUI | Sobreposição (overlay) dentro do app | Simples e portátil; janelas do SO podem vir depois |
| Plugin | Aplicador de presets com UI genérica do host (Entrada, Saída, Ligado); LV2 com um plugin por preset (Audacity), depois CLAP com lista dinâmica (Reaper) | Edição fica na app; a GUI Gio não encaixa em janela de host; a lista de opções do LV2 sai embaralhada no Audacity (19/11/2026) |
| Plugin: estado | Cópia do JSON do preset no projeto da DAW | O projeto soa igual mesmo se o preset mudar na app |
| Plugin: binário | Um único `.so` com todos os efeitos | Só um runtime Go por processo |
| Tone stack | Começa com biquads (RBJ); depois modelo Fender/Marshall | Funciona rápido; o modelo analógico é um ótimo estudo |
| FFT (IR) | FFT radix-2 própria em `dsp/fft.go` (antes: gonum) | Mantém `dsp/` sem dependências; é pequena e testada contra a DFT |
| Módulo Go | `github.com/tatianno/ampsim` | `ampsim` na Fase 1; renomeado ao publicar no GitHub (permite `go install`) |
| Convolução | Particionada com **latência zero** (partição 0 no tempo, demais por FFT) | Não soma latência ao vivo e independe do bloco do host |
| Oversampling | FIR polifásico 4x, 129 coeficientes, Kaiser β=7 | Latência inteira de 32 amostras (0,7 ms a 44,1 kHz) |

## 3. Arquitetura

```
 Fonte (Guitarra ao vivo | Arquivo WAV) ──► engine ──► Saída (fones/caixa)
                                             │   └──► Gravar / Exportar WAV
         [thread da GUI] ── parâmetros (atomics) ──► engine
         [thread da GUI] ◄── medidores (ring buffer) ── engine
```

**Cadeia de sinal (configurável)**

O amp é dividido em três módulos, e a cadeia é uma lista ordenável (inclusive com o áudio rodando):

```
[pedais de entrada] → PRÉ-AMP → [loop de efeitos (send/return)] → POWER AMP → [pós-power] → GABINETE → [pós-gabinete]
```

- **PRÉ-AMP** (`pre`): HP 70 Hz → GANHO/saturação 4x → tone stack (grave/médio/agudo)
- **POWER AMP** (`power`): saturação suave (`tanh`) → PRESENÇA → VOLUME
- **GABINETE** (`cab`): IR → low cut / high cut
- Regra: `pre` antes de `power`, e `power` antes de `cab`. Os pedais podem ficar em qualquer posição.
- Ordem padrão: `gate → pre → [chorus → flanger → phaser → delay → reverb] → power → cab` (pedais no loop).
- Reordenar com som rolando faz uma rampa de 5 ms até o silêncio, troca a ordem e sobe de novo (sem estalo).

### Estrutura de pastas

```
.
├── PLANO.md
├── go.mod            # módulo github.com/tatianno/ampsim
├── wav/              # leitura/gravação de WAV (PCM 8/16/24/32, float 32/64)
├── dsp/              # Go puro, sem dependências: filtros, saturação, oversampling, convolução, efeitos
├── engine/           # cadeia, tabela de parâmetros, suavização, presets/estado
├── cmd/
│   ├── ampcli/       # processa WAV offline (validação contra o Python)
│   └── ampapp/       # standalone: malgo + Gio
│       ├── audio/    # saída malgo, Renderer (fonte → engine → placa), Ring dos medidores
│       ├── source/   # LiveInput, FilePlayer
│       ├── ui/       # Gio: knob, pedal, cabeçote, gabinete, cadeia, transporte, medidores
│       └── gui.go    # a janela (gioui.org/app); gui_nogui.go = build só console
├── plugin/lv2/       # (fase 2) camada C + cgo sobre engine/
├── testdata/         # WAVs de entrada (DI de guitarra, IR do gabinete)
├── referencia_python/  # protótipos Python arquivados (referência para testes)
└── venv/             # ambiente Python da referência
```

### Regras do `engine/` (para servir ao app e ao plugin)

```go
type Processor interface {
    Prepare(sampleRate float64, maxBlock int) // toda alocação acontece aqui
    Process(in, out [][]float32, n int)       // n varia a cada chamada (1..maxBlock)
    Reset()                                   // limpa estado (delays, reverb, filtros)
    Latency() int                             // amostras (oversampling + IR)
}
```

- **Proibido dentro de `Process`:** alocar memória, mutex, goroutines, I/O, log, `time.Now`.
- **Sem taxa de amostragem fixa.** Coeficientes e reamostragem da IR são feitos no `Prepare`.
- **Sem tamanho de bloco fixo.** A saída precisa ser idêntica com blocos de 1, 37 ou 512 amostras.
- **Sem estado global.** Várias instâncias precisam conviver.
- **Parâmetros declarados numa tabela** (`ParamInfo{ID, Name, Min, Max, Default, Unit}`). Os valores ficam em atomics
  e são suavizados no DSP. A tabela gera os knobs da GUI e os parâmetros do plugin.
- **Estado serializável em JSON** (parâmetros, efeitos ligados, caminho da IR, ordem da cadeia). É o mesmo formato do
  preset do app e do state do plugin.

## 4. Parâmetros

### Amp (cabeçote = pré-amp + power amp, e o gabinete)

| Módulo | Knob | ID | Implementação |
|---|---|---|---|
| Pré-amp | Ganho | `pre.gain` | `drive` da saturação (de `main.py`) |
| Pré-amp | Grave | `pre.bass` | low shelf 100 Hz, ±12 dB |
| Pré-amp | Médio | `pre.mid` | peaking 700 Hz, ±12 dB |
| Pré-amp | Agudo | `pre.treble` | high shelf 3 kHz, ±12 dB |
| Power amp | Presença | `power.presence` | peaking 4 kHz, ±10 dB, depois do `tanh` |
| Power amp | Volume | `power.volume` | ganho de saída do amp (dB) |
| Gabinete | IR | (estado extra `ir`) | seletor de arquivo (padrão: `testdata/gabinete_2x12_sm57.wav`) |
| Gabinete | Low cut | `cab.lowcut` | passa-alta Butterworth 2ª ordem, 20–300 Hz (padrão 85) |
| Gabinete | High cut | `cab.highcut` | passa-baixa Butterworth 3ª ordem, 2–12 kHz (padrão 6200) |

### Pedais (a partir de `referencia_python/`)

| Pedal | Knobs | Origem / observação |
|---|---|---|
| Gate | Threshold · Release | `aplicar_noise_gate_rigido`; separar attack e release |
| Compressor | Threshold · Ratio · Attack · Release · Level · Mix + medidor de GR | novo (18/11, pedido após o MVP); `dsp.Compressor`, joelho suave de 6 dB, detector em dB, mix para compressão paralela |
| Chorus | Rate · Depth · Delay · Mix | `aplicar_chorus` |
| Flanger | Rate · Manual · Depth · Feedback · Mix | `min_delay`/`max_delay` viram Manual e Depth |
| Phaser | Rate · Range · Feedback · Mix + chave 4/6/8 estágios | `min_freq`/`max_freq` viram Range |
| Delay | Time · Feedback · Tone · Mix | `filtro_agudos` vira Tone; tap tempo como extra |
| Reverb | Size · Damping · Mix | Freeverb (`aplicar_reverb`) |

`cauda_segundos` (delay e reverb) não existe em tempo real; a cauda acontece naturalmente.

## 5. Interface (standalone)

```
┌────────────────────────────────────────────────────────────────┐
│ ENTRADA  (•) Guitarra [dispositivo ▾]  ( ) Arquivo [arquivo.wav] │
│          ▶ ⏸  ━━━━━●━━━━━━━  0:02/0:03  [↻ Loop]   Trim ◯  ▮▮▮▯ │
├────────────────────────────────────────────────────────────────┤
│ AMP    Ganho   Grave   Médio   Agudo   Presença   Volume       │
│        IR: [gabinete_2x12_sm57.wav ▾]                          │
├────────────────────────────────────────────────────────────────┤
│ CADEIA  [Gate] ▸ PRÉ ▸ ┊[Chorus][Delay][Reverb]┊ ▸ POWER ▸ CAB  │
│         (arrastar reordena; ┊…┊ = loop de efeitos; clique abre │
│          o pedal; o LED mostra se está ligado)                  │
├────────────────────────────────────────────────────────────────┤
│ [Bypass A/B]   [● Gravar]   [Exportar WAV]   Preset: [Clean ▾] │
│ Saída ▮▮▮▮▯▯   Latência: 5,3 ms                                │
└────────────────────────────────────────────────────────────────┘
```

**Janela do pedal** (overlay; fecha com Esc ou clique fora): corpo na cor do pedal, knobs gerados pela tabela de
parâmetros, nome, LED e footswitch. Clicar no amp abre o cabeçote (knobs do pré-amp e do power amp juntos) e o
gabinete (IR, low cut, high cut) no mesmo estilo.

**Cadeia na tela:** os pedais aparecem na ordem real, agrupados por trecho (`SectionOf`: entrada, loop, pós-power,
pós-gabinete). Arrastar um pedal chama `Chain.Move`; soltar entre PRÉ e POWER coloca no loop.

**Interação com os knobs:** arrastar na vertical, rodinha para ajuste fino, duplo clique para voltar ao padrão,
valor exibido enquanto gira.

## 6. Plano de ação (TODO)

> **MVP (primeira entrega):** Fases 0 a 4, ou seja, o app standalone tocando a guitarra **ao vivo** pelo amp e
> pelos pedais, com GUI. O que vem depois (Fase 5, Fase 6 e o suporte a NAM) é pós-MVP.
> **Status: MVP concluído em 18/11/2026** (latência de 31 ms aceita; débitos na seção 6.1).

### Fase 0: Preparação ✅
- [x] Corrigir o ruído no fim do `main.py`: normalizar antes da média estéreo e aplicar fade na DI
- [x] Arquivar os protótipos Python em `referencia_python/` e os WAVs em `testdata/`
- [x] Escrever este plano

### Fase 1: Núcleo de DSP + CLI ✅ (concluída em 17/11/2026)
**Meta:** `ampcli` processa `guitarra_di.wav` com amp, IR e efeitos, e o resultado bate com a referência Python.

- [x] Iniciar o módulo Go (`ampsim`) e o repositório git
- [x] `wav/`: leitura e escrita de WAV (PCM 8/16/24/32, float 32/64; normaliza **antes** da média dos canais)
- [x] `dsp/`: biquad (RBJ: LP, HP, shelf, peaking; Butterworth de ordem N em cascata) com troca de coeficientes ao vivo
- [x] `dsp/`: suavizador de parâmetro (one-pole)
- [x] `dsp/`: noise gate com envelope, attack e release separados e histerese
- [x] `dsp/`: oversampling 4x em streaming (FIR polifásico), latência de 32 amostras
- [x] `dsp/`: saturação (`tanh` com bias do envelope, como no `main.py`)
- [x] `dsp/`: tone stack (grave/médio/agudo) e presença
- [x] `dsp/`: convolução particionada com latência zero e preparação da IR (corte, fade, reamostragem, normalização)
- [x] `dsp/`: reamostragem offline (porte de `scipy.signal.resample_poly`)
- [x] `dsp/`: linha de atraso fracionária (buffer circular + interpolação)
- [x] `dsp/`: Chorus, Flanger, Phaser, Delay e Reverb em streaming
- [x] `engine/`: `Chain` com `Prepare/Process/Reset/Latency`, tabela `ParamInfo`, bypass por módulo com crossfade de 10 ms
- [x] `engine/`: estado/preset em JSON (inclui o caminho da IR)
- [x] `engine/`: troca de IR com o áudio rodando (preparada fora da thread de áudio, trocada por `atomic.Pointer`)
- [x] `cmd/ampcli`: `-in`, `-out`, `-ir`, `-preset`, `-salvar-preset`, `-on`, `-off`, `-set`, `-cauda`, `-bloco`, `-formato`, `-normalizar`, `-listar`
- [x] `referencia_python/gerar_referencias.py`: gera as saídas Python etapa por etapa em `testdata/referencia/`
- [x] Testes (`go test -race ./...`):
  - [x] convolução particionada igual à convolução direta (erro < -100 dB, blocos 1/37/64/512)
  - [x] **invariância de bloco:** cadeia completa com saída bit a bit idêntica com blocos de 1, 37, 64, 500 e 512
  - [x] **zero alocação:** blocos do `dsp` e `Chain.Process` (inclusive ligando/desligando pedal)
  - [x] comparação com a referência Python (tabela abaixo)
  - [x] fim do arquivo sem ruído (cauda em silêncio digital)
  - [x] latência constante com módulos em bypass; automação ao vivo sem NaN; preset ida e volta; troca de IR sem corrida

**Resultado contra o Python** (erro = RMS da diferença / RMS da referência):

| Bloco | Erro | Observação |
|---|---|---|
| Filtros Butterworth (HP 85 Hz, LP 6,2 kHz) | -152 dB | limite da precisão float32 das referências |
| Gate (dois ajustes do `main.py`) | -149 a -152 dB | com histerese 0 e attack = release |
| Preparação da IR (com reamostragem 48k → 44,1k) | -150 dB | |
| Gabinete (convolução + filtros) | -151 dB | |
| Chorus, Flanger, Phaser, Delay, Reverb | -152 dB | |
| Saturação (pré-amp 4x) | -64 dB | filtros de oversampling diferentes do `resample_poly` (que é de fase zero e não serve em tempo real) |

**Notas para as próximas fases**
- O amp no engine é: HP 70 Hz → pré-amp → tone stack → power amp (`tanh`) → presença → IR → HP 85 Hz / LP 6,2 kHz → volume.
  (Na Fase 2.5 virou três módulos: `pre`, `power`, `cab`.)
  O segundo gate do `main.py` (pós-amp) não entrou: com a normalização corrigida, o gate de entrada já basta.
- Volume padrão do amp em -12 dB: com a DI de teste e os knobs padrão, o pico fica em -3 dBFS.
- Desempenho: ~15x tempo real em um núcleo. Há folga, mas dá para ganhar com FFT real (metade do espectro) e uma
  aproximação mais barata de `tanh` (entrou como tarefa na Fase 5).
- Comandos úteis:
  - `go test ./...`
  - `go run ./cmd/ampcli -listar`
  - `go run ./cmd/ampcli -in testdata/guitarra_di.wav -out saida/amp.wav -on chorus,delay -set pre.gain=8`
  - `venv/bin/python referencia_python/gerar_referencias.py` (regenera as referências)

### Fase 2: Standalone com fonte Arquivo ✅ (concluída em 17/11/2026)
**Meta:** abrir um WAV, tocar em loop e ouvir o amp em tempo real.

- [x] `source/FilePlayer`: decodificar, converter para mono, reamostrar para a taxa da placa ao carregar; play, pause, posição, loop e trecho de loop
  - rampas de 5 ms em play/pause/seek e nas emendas do loop (sem estalo)
- [x] Saída de áudio com malgo (`github.com/gen2brain/malgo` v0.11.28): escolha do dispositivo, taxa e tamanho do buffer
- [x] Callback de áudio (`audio.Renderer`): fonte → engine → clip de segurança em ±1 → saída, com troca de fonte por `atomic.Pointer`
- [x] Ring buffer sem lock (`audio.Ring`) para os medidores: pico de entrada e saída, clips, gate aberto/fechado, carga de CPU por callback
- [x] Carregar a IR fora da thread de áudio e trocar de forma atômica (feito na Fase 1; comando `ir` no console)
- [x] `cmd/ampapp`: console interativo provisório (até a GUI da Fase 3) com comandos de reprodução, loop, `set`, `on/off`,
  presets, `status` e `medir` (medidores ao vivo)
- [x] Testes sem placa de som: player (fim do arquivo, loop com trecho, seek, rampas, zero alocação), render (estéreo,
  medidores, clip de segurança, zero alocação), ring concorrente
- [x] Teste na placa real (AudioBox, 48 kHz, buffer 256): callbacks estáveis, CPU de 9 a 18% do tempo do callback com amp e 3 pedais

**Notas**
- **Bug encontrado e corrigido:** `BypassFadeMs / 1000` entre constantes inteiras dava 0 em Go, então o crossfade de bypass da
  Fase 1 era instantâneo. As constantes em ms agora são float, e há um teste que mede a duração do crossfade.
- A latência exibida (2 períodos + cadeia) não inclui a latência interna do PipeWire/hardware. A medição real fica para a Fase 4.
- O malgo usa um mutex interno (sem disputa) em cada callback. Por enquanto não é problema; observar na Fase 4.
- Uso: `go run ./cmd/ampapp` (toca a DI em loop) · `go run ./cmd/ampapp -listar-dispositivos` · `-buffer 128` · `-arquivo x.wav`

### Fase 2.5: Cadeia configurável e loop de efeitos ✅ (concluída em 17/11/2026)
**Meta:** ordem dos módulos livre, com loop de efeitos (send/return) entre pré-amp e power amp. Feita antes da GUI
porque muda o modelo que a pedalboard e o cabeçote vão desenhar.

- [x] Amp dividido em três módulos: `pre` (ganho, grave, médio, agudo), `power` (presença, volume), `cab` (IR, low cut, high cut)
- [x] `Chain.SetOrder`, `Move`, `MoveBefore`, `MoveAfter`, `Order`, `SectionOf` (entrada / loop / pós-power / pós) e `String`
- [x] Validação: todos os módulos uma vez, `pre` antes de `power`, `power` antes de `cab`
- [x] Reordenar ao vivo: a ordem nova é trocada por `atomic.Pointer`, com rampa de 5 ms até o silêncio e de volta
- [x] Low cut / high cut do gabinete ajustáveis ao vivo, sem alocar
- [x] Preset versão 2: a lista de módulos vem na ordem da cadeia; presets da versão 1 (módulo `amp` único) são convertidos
- [x] CLI: `-ordem a,b,c...` e `-mover id:antes|depois:ref`; console do app: `ordem` e `mover <id> antes|depois <ref>`
- [x] Testes: validação da ordem, reordenar ao vivo (rampa + sem NaN + latência constante), loop × pós-gabinete,
  preset com ordem, migração v1 → v2, filtros do gabinete ao vivo sem alocação, descrição da cadeia

**Nota de DSP (medida com o delay):** mudar um efeito de lugar só faz diferença se houver algo **não linear** entre as
duas posições. Delay no loop × depois do gabinete: diferença de -30 dB (só a saturação leve do power amp separa os dois,
porque IR e filtros são lineares e comutam com o delay). Delay na frente do amp × no loop: -11,5 dB (as repetições passam
pela distorção do pré-amp). É por isso que, em amp com distorção, delay e reverb costumam ir no loop.

**Ideias para depois:** mais de uma instância do mesmo pedal (ex.: dois delays); um "master" do power amp que controle
o quanto ele satura.

### Fase 3: GUI ✅ (concluída em 17/11/2026)
**Meta:** tela principal completa com knobs e pedais, mexendo no som com o loop tocando.

- [x] Componente **Knob** (`ui/knob.go`): arrastar na vertical (180 dp = faixa inteira; Shift = 5x mais fino),
  rodinha (1% por clique), duplo clique volta ao padrão, valor formatado (`ui.FormatValue`) aparece forte ao passar o mouse
- [x] **Selector** para parâmetros discretos (estágios do phaser 4/6/8)
- [x] Tela principal (`ui/app.go`): barra de topo (dispositivo, taxa, buffer, latência, CPU), transporte, cabeçote,
  gabinete, cadeia e rodapé
- [x] Transporte (`ui/transport.go`): Abrir WAV… (zenity), Play/Pause (também na barra de espaço), Stop, Loop,
  barra de posição (clicar/arrastar = seek; Shift + arrastar = trecho do loop; duplo clique = arquivo inteiro)
- [x] Cabeçote (`ui/head.go`): knobs do pré-amp e do power amp num painel só, com liga/desliga de cada parte
- [x] Gabinete: botão da IR (zenity), Low cut, High cut, liga/desliga
- [x] Cadeia (`ui/board.go`) na ordem real, com o loop de efeitos destacado entre PRÉ e POWER
- [x] Arrastar pedais para reordenar (inclusive para dentro e para fora do loop); blocos do amp ficam fixos;
  marcador de onde o pedal vai cair e "fantasma" acompanhando o mouse; clique no LED liga/desliga
- [x] Componente **Pedal** genérico (`ui/pedal.go`): knobs gerados pela tabela (3 por linha), chaves, nome, LED, footswitch
- [x] Sobreposição: clicar no pedal da cadeia abre; fecha com Esc, clique fora ou "×"; cor por pedal (`ui/theme.go`)
- [x] Medidores (`ui/meters.go`): entrada e saída com queda suave, LED de clip (1 s), LED do gate, CPU
- [x] Bypass A/B global com crossfade de 10 ms, seco alinhado pela latência da cadeia (`audio.Renderer.SetBypass`);
  comando `ab` no console
- [x] `audio.Monitor` compartilhado entre console e GUI
- [x] Testes sem janela (`ui/ui_test.go`, com `input.Router` simulando mouse e teclado): knob (arrasto, Shift, duplo
  clique, rodinha), cadeia (arrastar para fora e para dentro do loop, bloco do amp não arrasta, clique abre, LED liga),
  seek e trecho de loop na barra, sobreposição (clique dentro não fecha, fora fecha, Esc fecha), carregar arquivo
  escolhido, desenhar a tela em 4 tamanhos e todos os pedais abertos
- [x] `-tags nogui` compila só o console (para máquinas sem as bibliotecas gráficas)
- [x] Abrir a janela real e ajustar o visual (capturas de tela da janela com `import -window`):
  rodapé preso embaixo, destaque do loop só quando é um trecho, medidor sem resto em silêncio, CPU só no rodapé,
  botão de fechar desenhado (o "×" da fonte era cortado), pedal com 4 knobs em 2 + 2, valores com mais contraste,
  arco a partir do zero em knobs bipolares (feedback −90..+90%)
- [x] `AMPSIM_ABRIR=<pedal>` abre um pedal ao iniciar (para conferir o visual)
- [x] Teste de uso de verdade (arrastar knobs e pedais com o som tocando): aprovado sem ajustes

**Para compilar a janela no Ubuntu** (uma vez):
```
sudo apt install libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev \
                 libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev
```
Depois: `go run ./cmd/ampapp` (janela) · `go run ./cmd/ampapp -console` (console).

**Notas**
- Bug achado pelos testes: a cadeia só registrava os filtros de evento dos blocos a partir do segundo quadro, e o
  primeiro clique era perdido. Corrigido (a ordem é conhecida desde a criação).
- A janela começa com o player pausado (Play ou barra de espaço); o console continua começando tocando.
- Presets na GUI (salvar/carregar/de fábrica) ficaram para a Fase 5, como planejado.

### Fase 4: Entrada ao vivo pela guitarra ✅ (concluída em 18/11/2026) — fecha o MVP
**Meta:** tocar ao vivo com latência total abaixo de ~10 ms. **Resultado:** 31,2 ms de ida e volta com buffer 64
(backend JACK próprio), aceito para os primeiros testes; o resto da redução virou débito técnico (seção 6.1).

- [x] `source.LiveInput`: recebe o buffer de entrada do callback (sem copiar), escolhe o canal (1, 2… ou média),
  aplica o **trim** (−24 a +24 dB, suavizado) e mede o pico e os clips do conversor antes do trim
- [x] `audio.Renderer.RenderDuplex`: entrada e saída no mesmo callback; medidor do nível da placa, clips do conversor e
  callbacks que passaram do tempo ("atrasos": prováveis estalos)
- [x] `audio.Host` (substitui o `Output`): abre em duplex, cai para só saída se a entrada falhar (com aviso), **reabre**
  com o app rodando (outra saída, outra entrada, outro buffer), backend escolhível (pulseaudio, alsa, jack)
- [x] `audio.Session`: cadeia, render, placa, fontes e medidores num lugar só (console e GUI usam a mesma)
- [x] **Teste de latência por loopback** (`Renderer.StartLatencyTest`): 5 pulsos curtos na saída, mede a volta pela
  entrada (limiar = 6× o ruído de fundo), mediana das medições; testado com loopback simulado (mede exatamente o atraso)
- [x] GUI: painel **ENTRADA** (Arquivo/Guitarra, canal, trim, nível da guitarra com a faixa ideal −18 a −6 dBFS,
  clip da placa) e painel **Áudio…** (saída, entrada, buffer 64–1024, aplicar, medir latência); latência medida no topo
- [x] Console: `fonte`, `canal`, `trim`, `latencia`, `dispositivos`, `saida`, `entrada`, `buffer`; opções `-entrada`,
  `-canal`, `-fonte`, `-backend`, `-saida` (antes `-dispositivo`)
- [x] Testes sem placa: canais, trim, leitura em pedaços, zero alocação; duplex, clips da placa; latência com loopback
  simulado e sem cabo; painel de entrada (troca de fonte, altura de uma linha) e painel de áudio
- [x] Teste na placa real, sem som: AudioBox abre em duplex (2 canais); nível da guitarra parada ≈ −48 dBFS;
  buffers 256, 128 e 64 abrem sem atrasos (cadeia com gate + amp: até 45% do tempo do callback em 64)
- [x] **Tocar a guitarra** (você, 18/11): ok
- [x] **Medir a latência real** (você, 18/11): **46,4 ms** de ida e volta (47,1 ms com a cadeia); estimada 16,7 ms
- [x] Teste de latência mais robusto (o teste falhou com a fonte em Arquivo): silêncio de 400 ms com o ruído medido só
  nos últimos 100 ms; detecção pelo pico da janela de 500 ms depois do pulso (chegada = primeira amostra acima de metade
  do pico), sem depender do volume de volta; diagnóstico (pico e ruído em dBFS) quando falha; testes com música em
  trânsito, retorno fraco, ruído e eco
- [x] `pipewire-jack` instalado; 1ª medida com `-backend jack`: **131,2 ms** (pior). Causas achadas com `pw-top`/`pw-link`:
  - sem pedir latência, o cliente JACK do PipeWire usa **1024 quadros** (21,3 ms por ciclo) e arrasta o grafo todo
    para 1024; 131 ms ≈ 6 ciclos;
  - o backend JACK do miniaudio se liga a **todas as portas físicas** (AudioBox + áudio interno): o som saía nas duas
    placas e a entrada tinha 4 canais (inclui o microfone interno).
- [x] Correções: no JACK o app pede o buffer escolhido via `PIPEWIRE_LATENCY` (respeita se o usuário já definiu) e
  escreve só no primeiro par de saídas (os demais canais ficam mudos), com teste
- [x] Medido de novo com o JACK do miniaudio (18/11): **36,8 ms** (buffer 128) e **26,0 ms** (buffer 64).
  Ajuste linear: **≈ 15 ms fixos + 8 × buffer**. Os 8 ciclos (o mínimo seria 2–3) vêm dos buffers intermediários
  do miniaudio; os ~15 ms fixos vêm da placa (AudioBox USB full speed) e de o PipeWire tratar entrada e saída dela
  como dispositivos separados (reamostrador entre eles).
- [x] **Backend JACK próprio** (18/11) — esperado ≈ 19 ms com buffer 64:
  - [x] cgo com `dlopen` da libjack (`audio/jack_linux.c` + `jack_linux.go`): sem headers para compilar; carrega a
    libjack do PipeWire direto (dispensa o `pw-jack`); sem JACK, o app volta para o miniaudio
  - [x] cliente `ampsim` com as portas de entrada da placa escolhida (até 8) e 2 de saída (L/R), ligadas **só** à
    placa escolhida (conferido com `pw-link -l`: só AudioBox)
  - [x] callback direto (`ampsimJackProcess`): entrada → `LiveInput` → engine → saídas, sem buffer intermediário
  - [x] buffer pedido ao PipeWire (`PIPEWIRE_LATENCY`) e lido de volta do JACK (o ciclo real aparece na tela)
  - [x] `Host` com escolha de backend: automático no Linux = JACK próprio se disponível, senão PulseAudio (com aviso)
  - [x] lista de placas do JACK (portas físicas agrupadas por placa, sem monitores) no painel Áudio… e no console;
    backend em uso mostrado no topo, no painel e no console
  - [x] teste na placa, em silêncio: ciclo 64 de verdade (`pw-top`), ~17 s sem nenhum atraso com 64 e 128
    (CPU mediana 13% / 9%); só a partida pode gerar 1 atraso
  - [x] bug achado: nomes das portas vazios ao listar sem cliente aberto (o JACK do PipeWire libera os nomes ao
    fechar o cliente temporário) — a lista agora é copiada antes
  - [x] medida com o backend próprio (18/11, buffer 64): **31,6 ms** (32,2 com a cadeia). Em dúvida: havia uma chamada
    com voz entrando durante a medição. Com o PipeWire (`pw-dump`), a saída da AudioBox estava com **headroom de 512
    quadros (10,7 ms)**, contra 64 nas medições anteriores, o que explica boa parte da diferença
  - [x] o teste agora confere se as 5 medições concordam (variação ≤ 1 ms); se não, avisa que o resultado não é
    confiável (testado com "voz" entrando numa janela)
  - [x] medido de novo em silêncio (18/11, buffer 64): **31,2 ms**, variação 0,7 ms (31,9 com a cadeia) — confirma
- [ ] → débito técnico (seção 6.1): fixar o headroom/período da AudioBox em 64 com regra do WirePlumber (só a AudioBox; reiniciar o WirePlumber
  interrompe o áudio do sistema por 1–2 s — fazer fora de chamadas). Esperado ≈ 22 ms. Arquivo proposto
  `~/.config/wireplumber/main.lua.d/51-audiobox-baixa-latencia.lua` com `api.alsa.period-size = 64` e
  `api.alsa.headroom = 64` para `alsa_*.usb-PreSonus_Audio_AudioBox_USB-01*`
- [x] Conta atual da latência com buffer 64: app ~3,3 ms (2 ciclos + cadeia) + headroom da saída 10,7 ms + entrada 1 ms
  + ~16,6 ms de driver USB do kernel, USB 1.1 e conversores (fora do alcance do app)
- [ ] → débito técnico (seção 6.1): testar o perfil **Pro Audio** da AudioBox no PipeWire
- [x] Meta de latência do MVP revista: 31 ms aceitos para os primeiros testes (18/11); alvo futuro ~20 ms com esta placa

- [x] Estalos: nenhum atraso em ~17 s com buffer 64 e 128 (só pode haver 1 na partida); otimização da cadeia segue na Fase 5

**Notas**
- **Investigação dos 46 ms** (18/11, com `pw-top`/`pw-dump`, app em silêncio): o grafo do PipeWire já roda com
  quantum **128** (2,7 ms) na AudioBox; o ALSA da AudioBox usa períodos de 64/128 com headroom 64/128; os streams do app
  negociam buffers pequenos (tlength 256 quadros, fragsize 128). O caminho de hardware explicaria ~12 ms. O resto vem da
  **camada PulseAudio** (pipewire-pulse) e do duplex do miniaudio sobre ela (dois streams separados sincronizados por um
  buffer intermediário). A saída é a API JACK do PipeWire: entrada e saída no mesmo ciclo do grafo.
- O PipeWire tem quantum padrão configurado de 1024 (≈21 ms) e mínimo 32 (na prática negociou 128). O app pede o buffer escolhido, mas a latência
  final depende do que o PipeWire concede: por isso a medida por loopback. Se ficar alta, dá para forçar com
  `pw-metadata -n settings 0 clock.force-quantum 128`.
- O JACK instalado é o jackd2 (sem servidor rodando); o backend usado é o PulseAudio (pipewire-pulse).
- Com buffer 64 a margem de CPU fica pequena com muitos pedais: a otimização da Fase 5 (FFT real, `tanh` mais
  barata) passa a ter efeito prático.
- Bugs achados: medidor da placa lido cedo demais no console (não era a placa: era o momento da leitura); `fill`
  ocupando a altura numa linha (painel de entrada esticava a tela) — separado em `hfill`/`vfill`, com teste.

### 6.1 Débitos técnicos

Registrados para resolver depois do MVP. Em ordem de ganho esperado.

| # | Débito | Detalhe | Ganho / motivo |
|---|---|---|---|
| D1 | **Latência: headroom da saída da AudioBox** | O PipeWire escolheu período e headroom de 512 quadros na saída da AudioBox (10,7 ms). Regra do WirePlumber em `~/.config/wireplumber/main.lua.d/51-audiobox-baixa-latencia.lua` com `api.alsa.period-size = 64` e `api.alsa.headroom = 64` para `alsa_*.usb-PreSonus_Audio_AudioBox_USB-01*`; reiniciar o WirePlumber (interrompe o áudio do sistema por 1–2 s; fazer fora de chamadas). Se estalar, usar 128. | ≈ 9 ms (31 → ~22 ms) |
| D2 | **Latência: perfil Pro Audio** | Testar o perfil Pro Audio da AudioBox no PipeWire (entrada e saída no mesmo relógio, sem reamostrador entre elas). | parte dos ~16 ms fixos |
| D3 | **Latência: meta original de ~10 ms** | Com esta placa (USB 1.1) e o driver USB do kernel, ~16 ms ficam fora do alcance do app. Rever com outra placa ou com ajustes do kernel (`snd-usb-audio`). | — |
| D4 | ~~Atraso na partida do JACK~~ (resolvido em 19/11) | Ao abrir ou reabrir a placa (os dois backends), o `Renderer` roda os primeiros `StartupSilenceMs` (100 ms) com a saída muda: a cadeia processa e aquece, e o som entra com a rampa de 10 ms do mudo. Um callback atrasado nessa janela (troca de ciclo do PipeWire, primeira chamada de Go na thread do JACK) fica em silêncio, e não conta no contador de atrasos nem na CPU máxima (`Meter.Warmup`). Na AudioBox, 6 reaberturas 64 ↔ 128 sem atraso (o binário antigo também não atrasou nessa rodada: o problema é intermitente). | — |
| D5 | **Backend miniaudio** | Continua como alternativa (PulseAudio/ALSA) com ~46 ms; no Linux o padrão é o JACK próprio. Avaliar se vale manter o caminho JACK do miniaudio (hoje removido). | simplicidade |
| D6 | ~~Texto da placa aberta no painel Áudio…~~ (aceito em 19/11) | A linha com backend, placas, taxa e buffer é cortada em janelas estreitas. O usuário considerou aceitável; fica como está. | — |
| D7 | ~~CPU com buffer 64~~ (resolvido em 18/11) | DSP 33% mais rápido; com amp + 8 pedais em buffer 64 no JACK, 0 atrasos. Os picos que restam vêm de agendamento e pausas do Go: se aparecerem estalos, avaliar `GOGC`/`debug.SetGCPercent` no app. | — |

### Fase 5: Recursos extras do standalone (~1 a 2 semanas)
- [x] **Pedal de compressor** (pedido em 18/11): Threshold, Ratio, Attack, Release, Level, Mix e medidor de redução de
  ganho no pedal; posição padrão entre o gate e o pré-amp (desligado); presets antigos ganham o compressor na posição
  padrão sem perder a ordem salva (`engine.MergeOrder`); testes de curva estática, nível, attack, mix seco, zero
  alocação, na cadeia e de migração de preset
- [x] **Presets** (18/11): pacote `presets/`
  - de fábrica gerados em código (Clean, Crunch, Lead, Ambiente), sempre com todos os módulos, sem IR (mantém o
    gabinete em uso), volumes acertados para ~−17 dBFS RMS com a DI de teste (sem salto ao trocar)
  - do usuário em `~/.config/ampsim/presets/*.json` (mesmo formato do `-preset`; nome dentro do arquivo; nome de
    arquivo seguro; nomes de fábrica protegidos; arquivo corrompido é ignorado com aviso)
  - GUI: linha PRESET com ◄ ►, nome (abre a lista), `*` quando modificado, Salvar, Salvar como… (campo de texto,
    Enter salva), Excluir com confirmação (2º clique em até 3 s)
  - console: `presets`, `preset <nome>`, `gravar <nome>`; `-preset` do app e do `ampcli` aceita arquivo ou **nome**
  - testes do pacote e da GUI (navegar, modificado, digitar o nome + Enter, lista, excluir), sempre em pasta temporária
- [x] **Gravar** (18/11): botão ● Gravar no rodapé (vira ■ Parar com o tempo, piscando); grava a **saída** e, com a
  chave **DI** (padrão ligada), a **entrada limpa** num 2º arquivo, para reamplificar depois; WAV 24 bits em
  `Música/ampsim/ampsim-AAAA-MM-DD_HH-MM-SS[-DI].wav` (`xdg-user-dir MUSIC`); a thread de áudio só copia para filas sem
  trava, uma goroutine escreve (`audio.Recorder`, `wav.StreamWriter` com cabeçalho corrigido no fim); avisa se perder
  amostras; fechar o app com gravação em curso fecha os arquivos direito; console `rec`
- [x] **Exportar** (18/11): botão Exportar… nos controles de arquivo; diálogo de salvar (zenity) com nome sugerido;
  processa o arquivo carregado com o som atual numa **cópia** da cadeia (o som ao vivo não muda), +2 s de cauda,
  latência compensada, WAV 24 bits, progresso no rodapé; `engine.RenderOffline` é o mesmo do `ampcli` (saída
  conferida idêntica bit a bit à de antes); console `exportar <arquivo.wav>`
- [x] **Retorno da gravação** (pedido em 18/11: sem aviso ao parar e sem lista, a gravação confundia): ao parar abre o
  painel **GRAVAÇÃO SALVA** (duração, o som e a DI, aviso de amostras perdidas) com a lista das gravações da pasta,
  mais novas primeiro (`audio.ListRecordings`, duração lida só do cabeçalho com `wav.Stat`); cada uma com ► Ouvir e
  **DI (reamp)**, que carrega no player, passa a fonte para Arquivo e toca; botão **Gravações** no rodapé abre a
  lista a qualquer hora; Abrir pasta (`xdg-open`); console `gravacoes`; rodapé reajustado para caber na janela
  mínima (com teste)
- [x] **Gravar só com a fonte em Guitarra** (relatado em 18/11: com a fonte em Arquivo o botão ficava ativo e gerou um
  arquivo mudo): com Arquivo o botão fica apagado e explica que o caminho é o Exportar…; o Parar sempre funciona;
  o gravador mede o pico (`Take.PeakDB`) e, se a gravação ficou muda (< −60 dBFS), o painel e o rodapé avisam em
  destaque; console `rec` com a mesma regra; testes
- [x] **► Ouvir não reprocessa** (dúvida do usuário em 18/11 revelou o defeito): a gravação principal já tem os efeitos;
  tocá-la de novo pela cadeia aplicava amp sobre amp. Agora ► Ouvir toca em A/B SECO (como foi gravada) e DI (reamp)
  passa pela cadeia; o SECO ligado pelo app desliga sozinho ao trocar de fonte ou de arquivo; textos do painel
  deixam claro "com efeitos" × "sem efeitos (DI)"
- [x] **Bug relatado (18/11): estalo no fim do arquivo exportado.** A DI termina num corte abrupto, que passando pela
  distorção virava um pico de −8,6 dBFS onde ela acaba; no play não aparecia porque o player faz rampa nas emendas do
  loop. Correção: rampas nas bordas do arquivo **ao carregar** (`dsp.FadeEdges`: 5 ms na entrada, 20 ms quadrática na
  saída), valendo igual para play, exportação e `ampcli`; teste de regressão que exporta a DI e confere o fim
  (comprovado: sem a rampa ele falha com os mesmos −8,6 dBFS)
- [x] `wav`: formato PCM 24 bits e gravação incremental; testes de gravação (2 s sem perdas, DI = entrada exata, sem
  alocação no render), exportação (tamanho e taxa) e pela GUI (botões, pastas temporárias, diálogos falsos)
- [x] **Tap tempo no Delay** (18/11): botão TAP no pedal (pisca no toque) e **tecla T** em qualquer lugar da janela
  (menos digitando nome de preset); média dos últimos 4 intervalos, recomeça após 2 s parado; subdivisões 1/4, 1/8.,
  1/8 e 1/16 (trocar mantém o BPM); BPM exibido; console `bpm 120 1/8.` (`engine.TapTempo`, testado)
- [x] Controles de arquivo (Abrir, Play, Stop, Loop, posição) agora ficam **abaixo** do painel ENTRADA e só aparecem
  com a fonte em Arquivo; a barra de espaço só dá play/pause nesse caso (pedido em 18/11; testado)
- [x] **Afinador** (18/11): detecção YIN (`dsp.PitchDetector`, de Cheveigné & Kawahara 2002) de 55 a 1400 Hz, janela
  de 2048 amostras, primeiro mínimo abaixo do limiar (evita erro de oitava) e interpolação parabólica
  - a thread de áudio só copia a entrada (antes da cadeia) para uma fila sem trava enquanto o afinador está aberto;
    a detecção roda numa goroutine ~25×/s (`audio.Tuner`)
  - GUI: botão Afinador no painel ENTRADA; nota grande (verde até ±3 cents, dourada até ±15), ponteiro −50..+50
    cents suavizado, frequência, cordas da afinação padrão, A4 de 430 a 450 Hz, **saída muda** (padrão ligado, com
    rampa) enquanto afina; console `afinar [segundos]`
  - testes: senos de Si 1 a Si 5 com erro < 0,5 cent, corda grave com harmônicos fortes sem erro de oitava, ruído e
    silêncio sem nota, nome da nota e cents (inclusive A4 = 432), afinador na GUI (abre, mostra G3, Esc fecha e
    devolve a saída), render sem alocação com afinador ligado; conferido também na DI real (linha melódica coerente)
- [x] **Tone stack modelado Fender/Marshall** (18/11), `dsp/tmb.go`, a partir do circuito (Yeh e Smith, DAFx 2006):
  - o circuito TMB passivo (C1–C3, R1–R4, pots de agudo, grave e médio) resolvido por **análise nodal com polinômios
    em s** (regra de Cramer): H(s) de 3ª ordem exato para cada posição dos knobs, sem fórmulas copiadas
  - componentes do Fender Bassman 5F6-A e do Marshall 1959; pot de grave logarítmico; bilinear (K = 2·fs); 3ª ordem
    em forma direta II transposta; ganho de compensação fixo por modelo (+8,1 dB Fender, +5,8 dB Marshall)
  - coeficientes recalculados a cada 32 amostras só enquanto um knob se move, com arrays fixos: **5,4 µs, 0 alocações**
  - validado: H(s) contra a solução direta do circuito (erro < 10⁻⁹); digital contra analógico (< 0,5 dB até 4 kHz);
    Fender todo em 0 ≈ mudo (−60 dB); vale nos médios com tudo em 5 (Fender −12,4 dB, Marshall −9,2 dB); cada knob mexe
    na sua região; estável (impulso decai) em toda a faixa
  - no pré-amp: parâmetro **Tone stack** (Moderno / Americano / Britânico, nomes sem marca desde 18/11) com crossfade de 20 ms ao trocar; seletor no
    cabeçote; o padrão continua Moderno (presets antigos não mudam); `ParamInfo.Choices` dá nome às posições
  - presets de fábrica: Clean e Ambiente com Fender, Crunch e Lead com Marshall (renivelados em −18 dBFS RMS)
- [x] **Otimização de CPU** (18/11), medida com benchmarks por bloco de 64 amostras (`dsp/bench_test.go`):
  - convolução: multiplica só a metade do espectro (simetria conjugada de sinais reais) e partição 0 com produto
    escalar de 4 acumuladores: **46,6 → 24,8 µs** (−47%)
  - pré-amp: oversampling polifásico com fases contíguas, decimação dobrada pela simetria do FIR, `log1p` só quando o
    drive muda, `tanh(bias)` 1× por amostra base (o profiler mostrou 68% do custo nos filtros, não no `tanh`):
    **27,5 → 17,3 µs** (−37%)
  - cadeia inteira com todos os pedais: **98,5 → 66,3 µs por bloco** (~5% do tempo de um bloco de 64)
  - precisão inalterada (saturação −63,8 dB e gabinete −151 dB contra o Python)
  - ao vivo (JACK, buffer 64, amp + 8 pedais ligados, ~22 s): **0 atrasos**; pior callback por janela de 200 ms:
    mediana 27%, p90 37%, máx 54% — os picos vêm de agendamento/pausas do Go, não do DSP
- [x] **Empacotamento Linux** (18/11): `scripts/empacotar.sh VERSAO` gera em `dist/`:
  - `ampsim_VERSAO_amd64.deb` (`/usr/bin/ampsim` e `ampsim-cli`, atalho no menu, ícone, `copyright` = MIT;
    `Depends` levantado das bibliotecas diretas do binário com `readelf` + `dpkg -S`; `Recommends`: pipewire-jack,
    zenity, xdg-user-dirs)
  - `ampsim-VERSAO-linux-amd64.tar.gz` com `install.sh`/`uninstall.sh` (pasta pessoal, sem sudo; atalho com caminho
    completo)
  - o app passou a funcionar fora da pasta do projeto: DI/IR padrão ausentes são ignoradas (erro só se pedidas
    explicitamente); **IR interna genérica** (`dsp.GenericCabIR`, curva de gabinete sintética, sem licença de
    terceiros) quando nenhuma IR é carregada; a janela abre mesmo se a placa falhar (aviso + escolher em Áudio…);
    versão no binário (`-versao`, título da janela)
  - ícone: o logo do usuário (`img/logo.webp`, combo + pedal), com o fundo e a sombra removidos (transparente), em
    PNG 48–512 px no tema `hicolor` (`packaging/linux/icons/`); também no topo do README
  - conferido: `.desktop` válido, metadados e permissões do `.deb`, instalação/remoção do `.tar.gz` numa pasta
    temporária rodando de fora do projeto
- [ ] Empacotamento Windows e macOS (ver "Áudio multiplataforma" abaixo)
- [ ] **Áudio multiplataforma** (decidido em 18/11; o `Host` escolhe o backend por sistema):
  - **Linux:** backend JACK próprio (PipeWire), com miniaudio (PulseAudio/ALSA) como alternativa
  - **macOS:** miniaudio → CoreAudio (baixa latência nativa; AudioBox é class-compliant). Se o duplex do miniaudio
    medir alto (unidades separadas + buffer), considerar um backend CoreAudio próprio
  - **Windows:** miniaudio → **WASAPI exclusivo** como opção no painel Áudio… (~10–15 ms; o compartilhado fica em
    ~20–30 ms). **ASIO** (padrão dos músicos; a PreSonus tem driver) fica como etapa separada: o miniaudio não suporta
    e a licença do SDK da Steinberg precisa ser avaliada para um projeto MIT
  - seletor de arquivo nativo em cada sistema (hoje usa `zenity`); build com cgo de cada plataforma (Xcode, MinGW)
  - teste de latência em cada sistema (precisa de alguém com a máquina e um cabo de loopback)

### Fase 6: Plugin "aplicador de presets" (~1 a 2 semanas)
**Redefinida em 19/11/2026.** O plugin **não edita** o som: ele aplica um preset da app a uma faixa (gravada ou
tocada ao vivo). A edição continua na standalone. Com isso a cadeia variável, os arquivos externos e a GUI deixam de
ser problema: a cadeia inteira vem do preset e a **UI genérica do host** basta. A GUI Gio não entra no plugin (nenhum
formato deixa o plugin ser dono da janela e do laço de eventos, que é como o Gio funciona).

**Controles:** Entrada (dB, trim: o nível da DI muda o quanto o NAM/drive saturam), Saída (dB) e Ligado (liga/desliga
com rampa; no LV2 é `lv2:enabled`, o host pode usar o próprio bypass).

**Estado salvo no projeto: cópia do preset.** O state guarda o **JSON completo** do preset. Ao reabrir o projeto, o
plugin usa a cópia, não o preset atual da app: a faixa soa como no dia da mixagem mesmo que o preset seja editado
depois. Capturas NAM e IRs de fábrica estão embutidas; as do usuário são referenciadas pelo caminho do preset.

**Uso ao vivo:** funciona em hosts que monitoram a entrada pelo efeito (Reaper, Ardour, Carla). O Audacity só aplica
na reprodução ou processando a faixa: nele o fluxo é gravar a DI limpa e aplicar o preset depois.

**Regras de implementação (valem para os dois formatos):**
- Nada de alocar ou ler arquivo em `run`/`process`; a cadeia é montada ao criar ou restaurar a instância.
- O host escolhe o tamanho do bloco e ele pode variar: blocos maiores que `Chain.MaxBlock()` são fatiados.
- Informar a latência ao host (porta de latência no LV2; `latency` no CLAP) para alinhar a faixa.
- Taxa do host: a cadeia é preparada na taxa que o host informar; o NAM já reamostra (`dsp.RateBridge`).
- Um único `.so` por formato (um runtime Go por processo).

#### Fase 6a: LV2 no Audacity (primeiro)
**Meta:** aplicar um preset a uma faixa DI no Audacity, como efeito e como efeito em tempo real.

**Um plugin por preset (decidido em 19/11/2026, depois do primeiro teste no Audacity).** O primeiro desenho tinha
um plugin com a lista de presets (enumeração) e posições genéricas da pedaleira ("Pedaleira 1·1" … "10·6"). No
Audacity 3.4.2 isso não serve: (1) a lilv devolve os pontos de uma enumeração em ordem de endereço de memória
(`lilv_ptr_cmp`), então a lista sai embaralhada, e o Audacity supõe ordem crescente para mostrar a opção atual
(`LV2ControlPort::Discretize`), mostrando o nome errado ao reabrir; (2) o Audacity não usa o state do plugin
(`mStateInterface` nunca é chamado; projetos e presets guardam só os valores das portas; o estado dos presets LV2
é ignorado, `// Ignore mpState`), então presets LV2 com o JSON não funcionariam. Agora cada preset é um plugin
("AmpSim: Clean", URI `…#fabrica-clean`; os do usuário `…#usuario-<arquivo>`), e o Audacity lista pelo nome no
menu de efeitos. A lista muda com os presets do usuário: o app reescreve os `.ttl` em `~/.lv2/ampsim.lv2`.

- [x] `plugin/` (núcleo, Go puro, testado sem C) + `plugin/lv2`: build `-buildmode=c-shared` + camada C fina
  (`lv2.c`, com as definições mínimas da API em `lv2min.h`, sem depender do lv2-dev). Ligado com `-z nodelete`
  (o runtime do Go não pode ser descarregado no `dlclose`). Um descritor por preset, mono
- [x] `.ttl` gerados por `plugin/lv2/ttl` (única fonte das URIs e do layout das portas); `ttlgen` para o pacote
- [x] App mantém o bundle do usuário (`ttl.Sync`, chamado por `App.OnPresetsChanged` a cada mudança da lista):
  reescreve só o que mudou, por troca atômica; com o `.so` do `.deb` cria um link; link quebrado apaga o bundle
- [x] Entrada, saída e liga/desliga (sinal seco atrasado pela latência da cadeia)
- [x] State LV2 com a cópia do JSON do preset (`atom:String`, chave `…#estado`), para os hosts que usam state
- [x] Teste sem DAW: `plugin/lv2/hostcheck/check.sh` (host em C pela lilv do sistema): saída igual ao render
  offline da app (diferença ≤ 2,4e-7 com a DI a 44,1 kHz, NAM reamostrado), latência informada (32/64
  amostras), estado salvo e restaurado com o mesmo som; preset do usuário carregado pela URI
- [x] Empacotar: o `.tar.gz` instala o bundle em `~/.lv2/`; o `.deb` instala o `.so` em `/usr/lib/ampsim/` (o app
  monta o bundle ao abrir; o `postrm` apaga os bundles que são só o link)
- [x] Conferir no Audacity (19/11/2026, pelo usuário): os efeitos aparecem no menu e funcionam
- [x] **Pedais e modulações sozinhos** (19/11/2026): um plugin por módulo ("AmpSim Pedal: Chorus", URI
  `…#pedal-<id>`), 17 ao todo (`plugin.Pedals`: a cadeia padrão sem pré, power e gabinete), com os knobs da tabela
  `ParamInfo` + Ligado. Fora: os parâmetros só de estéreo (`engine.StereoOnly`) e o drive NAM (tipo, entrada e
  tamanho: sem seletor de arquivo na UI genérica). Seletores sem `lv2:enumeration`: duas posições = `lv2:toggled`
  ("Velocidade (Rápido)"); mais = `lv2:integer` com a legenda no nome e `lv2:scalePoint` para os hosts que mostram.
  Nomes repetidos ganham o número da banda (EQ: "Freq 2"). Os knobs são lidos no `activate` e a cadeia é preparada
  com eles (sem a rampa a partir dos valores padrão). Sem state (o host guarda as portas). Teste: cada pedal com
  knobs fora do padrão soa igual ao módulo da app; `check.sh` carrega todos pela lilv
- [ ] Conferir os pedais no Audacity
- [x] Amps e IRs pelos presets (19/11/2026): banco de fábrica novo **Amps clássicos** (Americano Limpo, Americano
  Quebrando, Britânico Crunch, Britânico Saturado, High Gain Moderno, Moderno Limpo), só o amp simulado e o
  gabinete (+ gate nos de ganho alto), nomes sem marcas, grave no ponto neutro de cada TMB, volume a −18 dBFS RMS.
  Medido com senoide de 187,5 Hz no nível RMS da DI: as vozes Americano/Britânico saturam suave (THD ~5–7% no
  ganho 10; o High Gain ~19%); o Britânico Saturado leva um overdrive de boost (level 12) e chega a ~13%. A
  pedaleira.json que é só o padrão antigo (3 bancos de fábrica) passa a seguir o padrão atual (4 bancos)
- [ ] Testar também no Carla/Ardour (ao vivo; state com a cópia)

#### Fase 6b: Reaper (depois)
**Meta:** o mesmo plugin no Reaper, tocando ao vivo com monitoração.

**19/11/2026:** o Reaper (Linux) já reconhece e aplica os plugins LV2 da 6a (testado pelo usuário: presets e
pedais). O CLAP deixa de ser necessário para o Reaper ter o AmpSim; fica pelo que o LV2 não dá: estéreo, a lista
de presets num plugin só e a troca de preset com o áudio rodando. Atenção: os plugins LV2 são mono (1 entrada,
1 saída); numa faixa estéreo do Reaper, a saída vai só para o canal 1 e o canal 2 passa seco (ajustar os pinos ou
usar a faixa da DI em mono). Uma variante estéreo do LV2 resolveria sem esperar o CLAP.

- [ ] Formato: **CLAP** primeiro (API em C pura, MIT, suportada pelo Reaper; encaixa no `c-shared` do Go). VST3 só
  se houver demanda (API em C++, mais trabalho na camada)
- [ ] Um plugin só, **estéreo**, com o parâmetro Preset numa lista dinâmica (o CLAP pede o texto de cada valor ao
  plugin, então os nomes e a ordem são os da app) e a cópia do preset no state. Reaproveita o núcleo do 6a
  (Entrada, Saída, Ligado, state); volta a troca de preset com o áudio rodando: uma goroutine da instância aplica
  o preset na cadeia em uso (`Chain.ApplyState`, com a transição e o spillover da app), e o primeiro bloco monta
  na hora (render offline certo desde a primeira amostra). A primeira versão do 6a fazia assim (antes do commit)
- [ ] Testar ao vivo no Reaper com a AudioBox: latência informada, troca de preset por automação sem estalo
- [ ] (Longo prazo, opcional) GUI própria: janela Gio separada (LV2 external UI no Ardour/Carla); encaixada na
  janela da DAW só com outra biblioteca ou reparentando no X11

### Pós-MVP: suporte a modelos NAM (Neural Amp Modeler) (~1,5 a 2 semanas)
**Decidido em 18/11/2026:** fica para depois da entrega do MVP. A ordem em relação às Fases 5 e 6 será decidida então.
Depende do trim de entrada da Fase 4 (o modelo é sensível ao nível de entrada).

**Meta:** carregar um arquivo `.nam` e tocar com ele no lugar do amp simulado.

**O formato:** JSON com `architecture` (quase sempre `WaveNet`; às vezes `LSTM`; raramente `ConvNet`/`Linear`),
`config` (camadas, canais, kernel, dilatações), `weights` (vetor com todos os pesos), `sample_rate` (normalmente
48 kHz) e `metadata` (nome, calibração de nível de entrada/saída).

**Decisões e abordagem**
- **Go puro** (sem cgo/C++), no espírito de estudo e para não complicar o plugin LV2.
  Alternativa se o desempenho não fechar: cgo com a NeuralAmpModelerCore (C++, MIT, usa Eigen).
- **WaveNet genérico**, guiado pela `config` do arquivo: o mesmo código roda os tamanhos standard, lite, feather e
  nano. Suportar "só o lite" não simplifica o código; o lite ajuda na CPU.
- **Começar validando com capturas lite/feather** (rodam com folga em tempo real); depois medir a CPU com standard
  e otimizar se precisar.
- **LSTM fica para depois** (bem menos comum).

**Tarefas**
- [x] `nam/`: leitura do `.nam` (JSON) e validação da `config` (18/11/2026)
  - formatos: WaveNet clássico (A1) e o novo 0.7 (A2: `kernel_sizes` por camada, `bottleneck`, `layer1x1`/`head1x1`,
    cabeça com kernel > 1, `gating_mode` none/gated/blended, ativações LeakyReLU/PReLU/SiLU/Softsign etc., grupos,
    cabeça pós-pilhas) e **SlimmableContainer** (vários WaveNets do mesmo som; o tamanho 0..1 escolhe um)
  - recusa com mensagem clara: LSTM/ConvNet/Linear, FiLM ligado, `condition_dsp`, WaveNet slimmable por canais
  - os arquivos reais da TONE3000 (`NAM/`, fora do git) são containers com um **lite de 3 canais** (tamanho < 0,5)
    e um **completo de 8 canais**, 23 camadas, campo receptivo de 6347 amostras (132 ms)
- [x] WaveNet em streaming (`nam/wavenet.go`): float32 como a NAM core, em blocos; buffer linear por convolução
  (taps lidos como fatias contíguas); somas de 4 entradas por passada; **zero alocação** em `Process`/`Reset`
  - **prewarm** como na NAM core (silêncio por todo o campo receptivo); o estado aquecido fica guardado e o `Reset`
    só o copia de volta (barato, pode ser chamado da thread de áudio)
  - testes: streaming × implementação direta (não-streaming, float64) em modelos sintéticos A1, A2 e um "misto"
    com gating/blending/head1x1/grupos/cabeça; blocos de 1 a 4096; ordem dos pesos (PyTorch); erros de validação
- [x] Validado contra a inferência oficial (18/11): ferramenta `render` da NeuralAmpModelerCore (commit d3189ed,
  compilada no scratchpad), mesma entrada (DI de `testdata/` reamostrada para 48 kHz), `cmd/namcli` do lado Go

  | Modelo | Go × NAM core | NAM core × ela mesma (atalho A2 × genérico) |
  |---|---|---|
  | Warm Music clean, lite | erro máx 4,9e-7, SNR 123 dB | 5,9e-7, 121 dB |
  | Warm Music clean, completo | 4,2e-7, 125 dB | 3,3e-7, 127 dB |
  | EVH 5150 high-gain, lite | 1,6e-4, 91 dB | 1,6e-4, 91 dB |
  | EVH 5150 high-gain, completo | 6,6e-5, 100 dB | 2,4e-5, 107 dB |

  O high-gain amplifica o arredondamento de float32: a diferença Go × oficial é do tamanho da diferença entre os
  dois caminhos de código da própria NAM core. As saídas de referência ficam em `testdata/nam/` (fora do git) e o
  teste `TestReferenciaOficial` as usa quando presentes
- **CPU medida** (i5-10310U, bloco 256, um núcleo): **lite 4,4%** do tempo real, **completo (8 canais) ~23%**.
  O laço de multiplicação-e-soma (`axpy4`) é 76% do tempo, aritmética escalar pura; somar 8 entradas por passada
  não ajudou (falta de registradores). Próximo passo de otimização, se precisar: SIMD (`simd/archsimd`, experimental)
- [x] **Amp NAM no engine** (18/11/2026): em vez de um bloco novo, o **pré-amp ganhou o modo** `amp` =
  Simulado | NAM (`engine/namamp.go`). A ordem da cadeia, o loop de efeitos e os presets não mudam: no modo NAM o
  modelo substitui o pré-amp, o **power amp fica só com o volume** (a captura já inclui o power) e o loop fica entre
  o NAM e o gabinete
  - parâmetros novos do `pre`: `amp`, `nam_input` (ganho de entrada, −24 a +24 dB) e `nam_size` (Lite | Completo);
    o modelo vai no estado como `extra.nam` (`embutida:<nome>` ou caminho do `.nam`)
  - troca de modelo, de tamanho e de modo **ao vivo, com crossfade de 20 ms e sem alocar**: as redes de cada
    submodelo são montadas e aquecidas fora da thread de áudio; trocar só copia o estado aquecido
  - latência compensada: a saída do NAM é atrasada em 32 amostras para ficar igual à do amp simulado (a latência
    da cadeia continua constante)
  - **normalização pelo `loudness` dos metadados** (como o "normalize" do plugin oficial), alvo −12 dB. Com o
    gabinete, as capturas limpas ficam ~1,5 dB acima do amp simulado e as mais saturadas até ~5 dB abaixo (o
    loudness dos metadados é medido sem gabinete; a IR corta os agudos da distorção)
  - testes: trocas ao vivo (sem estalo, saída idêntica com blocos de 64/512/1024), zero alocação, preset guarda o
    modelo, erro ao carregar não troca o modelo, latência alinhada, volume perto do simulado
- [x] **Capturas incluídas** (`capturas/`, `go:embed`): 11 capturas do autor (Warm Music 2x8, só o amp, sem
  gabinete), lite + completo cada; ~3,2 MB no binário. Modelo padrão: `Dist Sat5 V4 B5 M5 T5 P5`. Descrição e
  legenda dos nomes em `capturas/README.md`; licença MIT (decidido pelo autor em 18/11)
- [x] **IR padrão embutida**: a `gabinete_2x12_sm57.wav` (do autor, MIT) vai em `capturas/` e substitui a IR genérica
  sintética quando nenhuma IR é escolhida (a genérica fica só de reserva); `-ir` vazio por padrão no app e no CLI
- [x] **IRs de fábrica** (18/11): 1x10 SM58, 2x12 SM57 (padrão) e 4x12 SM7, do autor (MIT), em `capturas/`; o gabinete
  abre a lista "GABINETE (IR)" (incluídas + "Abrir arquivo WAV…"); o estado guarda `embutida:<nome>`; `-ir` aceita
  o nome. Corrigido o `PrepareIR`: o fade de 25 ms do fim fica no último quarto de IRs curtas (a 4x12 tem 16 ms e
  seria atenuada inteira)
- [x] O app **abre no modo NAM** (os presets de fábrica continuam no amp simulado, para o qual foram acertados);
  CLI e testes continuam com o simulado por padrão
- [x] Área **Sobre** (botão no topo): ícone, versão, repositório e contato do autor, com botões que abrem o
  navegador e o e-mail (`xdg-open`); crédito ao Neural Amp Modeler
- [x] **EQ paramétrico** (18/11, `engine/eq.go`): pedal `eq` com 5 bandas (shelf de graves 40–500 Hz, médios
  80–2k / 200–5k / 500–10k com Q 0,3–10, shelf de agudos 1,5–12 kHz; ganho ±15 dB) e level ±12 dB; um único pedal,
  arrastável como os outros, padrão no início do loop e desligado. Knobs de frequência e Q em escala logarítmica
  (`ParamInfo.Log`). Coeficientes recalculados só quando um knob muda, sem alocar. Pedal próprio na GUI: curva de
  resposta (20 Hz–20 kHz, ±18 dB, calculada por `EQ.ResponseDB`) e uma coluna por banda. Testes: resposta calculada
  × medida com senos, plano = transparente, zero alocação, knob logarítmico
- [x] **Tremolo e vibrato** (18/11, `dsp/modulation.go`): tremolo com Rate 0,5–15 Hz, Depth 0–100% e Forma
  (Seno | Triângulo | Quadrada suavizada; ganho suavizado em ~1,5 ms, sem estalo); vibrato 100% molhado por linha de
  atraso modulada com interpolação cúbica (Hermite; não abafa os agudos), Rate 0,5–12 Hz, Depth 0–2,5 ms, latência
  0 como o chorus. Padrão no loop, depois do phaser, desligados. Testes: depth 0 = transparente/atraso puro, ganho de
  0 a 1 no ritmo certo nas 3 formas, desvio de tom medido = 2π·rate·depth, 8 kHz sem perda. Com 13 blocos, o rótulo
  do bloco diminui a fonte ou abrevia (FLANG, VIB, TREM...) quando não cabe
- [x] **Adicionar e remover pedais** (18/11): a ordem da cadeia é a lista dos módulos presentes (todos existem e são
  preparados; só os presentes processam). `Chain.Add` (posição padrão pela `DefaultOrder`, ligado), `Remove` (as
  partes do amp não saem), `Available`, `AllModules`; a troca usa a rampa de reordenação e o módulo que entra começa
  limpo (Reset, sem alocar). Latência = só dos presentes. Preset **versão 3** (só os presentes); v2 abre com os
  ligados + o amp; v1 na ordem padrão. O app abre com `StartOrder` (gate + amp); presets de fábrica só com os pedais
  usados. GUI: bloco "+" no fim da cadeia → "ADICIONAR PEDAL" (grupos); "Remover" em cada pedal; `on <pedal>`
  no console adiciona
- [x] **Novos pedais** (18/11), `dsp/drive.go`, `wah.go`, `octave.go`, `rotary.go`, `pitchshift.go`:
  - **Drive**: Boost (até +20 dB limpo), Overdrive (Tube Screamer: passa-alta de 720 Hz antes do tanh e o limpo
    somado), Distorção (RAT: até 600x, joelho duro), Fuzz (assimétrico, vale nos médios); ceifamento a 4x (latência
    de 32 amostras, só com o drive na cadeia); Tone = tilt ±6 dB em 1 kHz; níveis calibrados (Drive 5 e Level 0 dB =
    nível da entrada); troca de modo com crossfade
  - **Wah**: SVF TPT (Simper) passa-banda de 350 Hz ao topo da Faixa (1,2–3,5 kHz), Envelope / LFO / Fixo
  - **Octave**: divisores por 2 em cascata (flip-flop com histerese sobre o sinal filtrado, estilo OC-2) e +1 oitava
    por retificação
  - **Rotary**: divisor Linkwitz-Riley em 800 Hz; corneta e tambor com Doppler (atraso modulado, interpolação
    cúbica) e volume modulado; 0,8/6,7 Hz e 0,67/5,9 Hz com inércia diferente
  - **Vibe**: 4 passa-tudo escalonados (90 Hz a 3,8 kHz) com LFO de lâmpada; Chorus ou Vibrato
  - **Pitch**: 2 vozes (±12 st) e detune; leitura de linha de atraso com **emendas sincronizadas** (o salto de maior
    correlação, como no SOLA) e crossfade de 5 ms: afinação exata (a versão de 2 janelas fixas saía ~1% desafinada
    em tons puros); atraso do molhado de 4 a 45 ms
  - CPU (um núcleo, bloco 256): drive 1,3%, pitch 1,0%, vibe 1,0%, wah 0,8%, rotary 0,7%, octave 0,1%
  - testes de DSP: boost limpo; harmônicos crescem com o drive; wah com o pico na posição certa e o envelope abrindo
    com a palhetada; octave com 110/55/440 Hz dominando para 220 Hz; rotary acelerando com inércia e pulsando;
    vibe passa-tudo no vibrato e variando no chorus; pitch +12/−12/+7/0 st no lugar certo
- [x] **Release v0.8.0** (18/11/2026): presets de fábrica novos e pedaleira
- [x] **Presets de fábrica novos** (18/11): Clean, Crunch, Blues, Rock Clássico, Solo Hard Rock (estilo Slash),
  Metal, Funk, Country, Surf, Psicodélico, Modulação, Worship, Ambiente e Synth. Cada um escolhe o amp (captura NAM
  incluída ou o simulado), o gabinete (IR incluída) e os pedais; o estado extra só aponta para capturas/IRs
  embutidas. Volumes calibrados com a DI (`TestFabricaNoMesmoVolume`): −18 dBFS RMS, sem passar de −1 dBFS de pico;
  os limpos e dinâmicos (Clean, Funk, Country) ficam ~2 dB abaixo, limitados pelo pico (fator de crista ~20 dB)
- [x] **Pedaleira de presets** (18/11, `cmd/ampapp/ui/pedalboard.go`): abaixo da cadeia, 6 footswitches por banco
  (◀ BANCO ▶), LED no preset atual, teclas 1 a 6; o banco acompanha o preset atual. Some em janelas com menos de
  720 dp de altura (o rodapé tem prioridade; as teclas continuam valendo). A dica da cadeia subiu para a linha do título
- [x] **Release v0.13.0** (19/11/2026): grave dos TMB com a curva do circuito, limitador de segurança na saída,
  reamostragem em tempo real do NAM (placa a 44,1 kHz), calibração de entrada por `input_level_dbu` e partida
  silenciosa ao abrir a placa (D4)
- [x] **Release v0.12.1** (19/11/2026): NAM com núcleos SIMD (AVX2 + FMA), modelo completo ~3× mais rápido
  (os atrasos com a Sat7 vinham do amp no Completo)
- [x] **Release v0.12.0** (19/11/2026): spillover na troca de preset (botão na pedaleira) e saída estéreo
  (modulações e ambiências com Largura, delay Duplo/Ping-pong, botão Estéreo na cadeia, gravação e exportação
  em 2 canais)
- [x] **Release v0.11.0** (19/11/2026): Drive com tipo NAM e 10 capturas de pedal (TS7 e SD-1), presets Rock
  Clássico, Blues, Worship e Solo Hard Rock com o drive NAM, voz High Gain no amp simulado (Metal passa a usá-la),
  correções no carregamento de presets antigos e no `ampcli -drive-nam`
- [x] **Release v0.10.0** (19/11/2026): menu (áudio, afinador, backup/restauração, atalhos, sobre), analisador
  de espectro, cortes do gabinete no EQ, EQs do teste ao vivo, 4 presets novos em 3 bancos temáticos, pedaleira com
  nomes de banco e edição em matriz, knobs por modo no Drive (Fuzz Face e Muff) e no Wah
- [x] **Release v0.9.0** (18/11/2026): pedaleira configurável
- [x] **Pedaleira configurável** (18/11): botão "Editar" → clicar num footswitch abre a lista de presets (fábrica,
  meus e "Vazio"); "Restaurar padrão" (2 cliques) volta à ordem da lista e apaga a configuração. Salva em
  `~/.config/ampsim/pedaleira.json` (nomes dos presets; preset apagado aparece "(apagado)" e não faz nada); mínimo
  de 3 bancos; o banco acompanha o preset atual pelo footswitch que o contém
- [x] **Teste ao vivo com a guitarra** (você, 19/11): impressão boa; atraso sutil no início (ver abaixo); EQs
  ajustados nos presets com médios/limpos
- [x] **EQs dos presets a partir do teste ao vivo** (19/11): o ajuste ao vivo cortava forte acima de 4–5 kHz e
  reforçava 150–800 Hz, mas com Level em 0 (+5,5 a +7,7 dB de volume; pico acima de 0 dBFS no Worship e no Country)
  e com bandas sem efeito (graves −15 dB em 40 Hz, abaixo do low cut de 85 Hz do gabinete; agudos em 12 kHz, acima
  do high cut de 6,2 kHz). Aplicado ~60% do rumo, com o Level compensando: Worship, Country, Solo Hard Rock e Metal
  no mesmo volume de antes (conferido com a DI)
- [x] **EQ: cortes do gabinete no gráfico** (19/11): low cut e high cut do gabinete (se ligado) como linhas
  tracejadas azuis e sombra além delas, com o valor; rótulo "0" na curva
- [x] **Menu** (19/11, `cmd/ampapp/ui/menu.go`): o botão "Sobre" virou "Menu" com Áudio…, Afinador (tecla A),
  Backup dos presets…, Restaurar presets… e Sobre; Afinador e Áudio… saíram da linha da ENTRADA
- [x] **Presets New Wave e Pop Anos 80** (19/11, pedido do teste ao vivo): chorus estilo The Police (Clean V10 G,
  4x12, chorus largo 0,7 Hz/3,5 ms, eco de 400 ms) e estilo Roxette (Clean V10, 2x12, compressor firme, chorus
  1,1 Hz/2,2 ms, delay de 300 ms, sala grande); no fim da lista (a pedaleira padrão não muda de lugar os outros 14);
  limpos, limitados pelo pico: −19,1 e −19,7 dBFS RMS
- [x] **Presets Treble Booster e Fuzz Oitavado** (19/11): fecham o banco 3 (18 de fábrica = 3 bancos; os presets
  do usuário começam no banco 4 na pedaleira padrão). Treble Booster = estilo Brian May (Boost com tone alto na
  captura Sat3 MD, 2x12, EQ −4 dB em 150 Hz / +3 dB em 1,2 kHz / −6 dB acima de 5,5 kHz, delay 350 ms); Fuzz
  Oitavado = estilo Hendrix/Octavia (+1 oitava 60% no Fuzz, Sat3 MD, 4x12, reverb curto). Ambos em −18,0 dBFS RMS
- [x] **Presets de fábrica por tema** (19/11): um tema por banco da pedaleira padrão (`presets.FactoryBanks`),
  com o ganho subindo dentro do banco: Limpos (Clean, Country, Funk, Surf, New Wave, Pop Anos 80), Drive e rock
  (Blues, Crunch, Rock Clássico, Treble Booster, Solo Hard Rock, Metal), Efeitos e texturas (Psicodélico, Fuzz
  Oitavado, Modulação, Worship, Ambiente, Synth). A pedaleira mostra o tema abaixo do "BANCO n/3" (só na ordem
  padrão; personalizada, não). Teste garante 6 por banco
- [x] **Pedaleira: nomes dos bancos e edição em matriz** (19/11): o tema dos bancos sumia ao trocar um preset
  (estava ligado à ordem padrão). Agora o nome é do banco e fica no `pedaleira.json` (`banks`), com os temas de
  fábrica como padrão; a primeira edição copia ordem e nomes para a configuração. Entre ◀ ▶ aparece o nome (ou
  "BANCO n/N" se não tiver), e embaixo "banco n de N · teclas 1 a 6". "Editar" abre a matriz: nome editável por
  banco (até 18 letras; atalhos desligados enquanto digita), 6 células (clique → escolha do preset, com os de fábrica
  em colunas por tema e "Voltar"), "+ Banco" (até 10), "− Último banco" (vazio, acima de 3) e "Restaurar padrão" (2
  cliques). Configuração antiga sem `banks` = bancos sem nome. Testes de nomes, digitação, bancos e restaurar
- [x] **Drive: knobs de cada modo como no pedal de referência** (19/11): `engine.ParamViewer` diz quais knobs
  valem no modo e com que nome; a GUI mostra só esses, e os escondidos não atuam no som (teste). Boost = Boost e Tone
  (Level neutro); Overdrive = Drive, Tone, Level; Distorção = Distortion, Tone, Level; **Fuzz** agora é Fuzz Face
  (ceifamento assimétrico, graves cheios, sem o vale nos médios, Tone plano) = Fuzz e Volume; modo novo **Muff**
  (Big Muff: dois estágios simétricos `tanh(2·tanh)`, vale em 900 Hz) = Sustain, Tone, Volume. Normalização das vozes
  novas medida como as outras (Drive 5 = nível da entrada). Presets: Treble Booster com Boost 9 (+18 dB, mesmo
  volume de antes), Psicodélico e Fuzz Oitavado no Fuzz Face, volumes recalibrados (−18,0 dBFS). Seletor com 5 ou
  mais posições põe o nome numa linha acima
- [x] **Drive com captura NAM de pedal** (19/11): o Drive ganhou a chave **Tipo: Modelado | NAM**. No NAM, a
  captura substitui os 5 modos (o Modo some e aparece o Tamanho Lite | Completo); knobs Entrada (−24 a +24 dB, antes
  do modelo) e Level (depois); botão "CAPTURA NAM" no pedal abre a lista "CAPTURAS DE PEDAL" (incluídas + "Abrir
  arquivo .nam…") e escolher volta ao pedal. Estado: `drive.type`, `nam_input`, `nam_size` e `extra.nam` (só no
  tipo NAM; os presets do modelado não mudam); o backup leva o `.nam` do usuário como já fazia com o do amp
  - `engine/namamp.go`: o motor virou `namBlock`, usado pelo pré-amp e pelo drive, com latência, normalização e
    modelo padrão por instância. **Pedal sem normalização pelo loudness**: o ganho aprendido (TS9 D0 = +6 dB, SD-1
    cranked ≈ +10 dB) é o quanto o pedal empurra o amp; e o `loudness` dos metadados mudou entre duas versões do
    mesmo arquivo (pesos idênticos), então não é confiável
  - latência: a resposta ao impulso das 10 capturas começa nas mesmas 32 amostras do modelado (o pico vem 20–45
    amostras depois: são os filtros e o clipping do pedal, não latência)
  - troca de tipo com crossfade de 20 ms (o modelado que sai continua em `cur` quando o novo é o NAM)
  - `engine.SelectorViewer`: as chaves que valem no modo (Tipo + Modo, ou Tipo + Tamanho)
  - **10 capturas de pedal embutidas** em `capturas/pedais/` (do autor, MIT): Ibanez TS7 nos modos TS9 e HOT (9
    ajustes, Level 5 em todas) e Oliver SD-10 (SD-1) cranked; padrão `TS7 TS9 D5 T5 L5 (Medium Drive)`. Nomes sem o
    travessão do original (o `go:embed` só aceita letras e pontuação ASCII); `capturas.PedalNames()`
  - `ampcli -drive-nam "<captura>"` (reamostra para 48 kHz como o `-nam`); `-listar` mostra as de pedal
  - CPU (i5-10310U, blocos de 64): cada rede custa o mesmo que a do amp, ~5% no Lite e ~27% no Completo; drive NAM
    + amp NAM Completos ≈ 54% (o caso dos atrasos com a Sat7). O drive começa no Lite
  - testes: trocas ao vivo (sem estalo, saída idêntica com blocos de 64/512/1024), zero alocação, ganho da captura
    preservado e Level depois do modelo, latência alinhada nas 10, preset guarda a captura só no NAM, GUI (chaves,
    knobs, lista, arquivo do usuário, volta ao pedal). Visual conferido em render headless
- [x] **Rock Clássico com o SD-1 NAM** (19/11): drive no tipo NAM com `SD-1 Oliver SD-10 D10 T6 L10 (Cranked)`
  (Level 0) na frente da Dist Sat3 MD; volume −8 → −10,7 dB (−17,9 dBFS na DI). O Level do pedal quase não muda a
  saturação (crista ~11,1 dB com 0, −4 ou −8 dB: o SD-1 com Drive 10 já ceifa), entre o Rock Clássico antigo (12,7)
  e o Solo Hard Rock (10,8). `mk` dos presets aceita a captura de pedal
- [x] **Blues, Worship e Solo Hard Rock com o TS7 NAM** (19/11): Blues e Worship com `TS7 TS9 D3 T7 L5
  (Light Drive, Bright)`, Solo Hard Rock com `TS7 TS9 D0 T0 L5 (Clean Boost)` de boost no Sat7 MD. O **Metal voltou
  ao modelado** (o usuário achou estranho ao vivo): o boost modelado corta o grave antes do amp (−9 dB em 80 Hz,
  −6,8 em 200) e realça 2–3 kHz (+4,7); a TS9 D0 T0 é plana no grave e escura (−9 dB em 3 kHz). No preset inteiro,
  a captura deu +4,3 dB em 80 Hz (grave embolando no Sat7 MD) e −7 dB em 1,25 kHz. A mais próxima (TS9 D5 T7,
  Entrada −6) acerta o grave mas segue −6 dB em 1,25 kHz. Com o drive no zero, o TS real passa o sinal quase todo
  pelo caminho limpo (o corte de graves fica no ramo do ganho): para um "boost de metal" NAM, capturar com mais
  drive ou aceitar e compensar no EQ. Level do
  drive acertado para o mesmo empurrão no amp e volume recalibrado (−18,0 a −18,2 dBFS). Drive/Tone/Modo ficam com
  o ajuste do modelado anterior, para comparar trocando o Tipo. Medido por FFT (bandas de 60 Hz a 8 kHz):
  - Solo Hard Rock e Metal quase não mudam de timbre (±1,5 a 2,5 dB nas bandas; um pouco mais comprimidos)
  - **Blues e Worship ficam bem mais escuros e com mais médios**: −4 a −5 dB em 1–2,5 kHz e −8 a −13 dB acima de
    2,5 kHz. É o TS de verdade com pouco drive: as capturas TS9 D0 T0 e D3 T7 cortam −7 a −10 dB acima de 3 kHz em
    relação à DI (o "Bright" T7 não fica mais claro que o T0); as de mais drive enchem os agudos com harmônicos, e a
    SD-1 e as do HOT mostram conteúdo até 8 kHz (a cadeia de captura não corta). EQ não compensado: decidir de ouvido
  - a métrica de bandas com filtros de 1 polo (usada na primeira comparação) vaza entre bandas e não via shelves
    acima de 4,5 kHz; as diferenças de agudo de "2 a 3 dB" daquela comparação estavam subestimadas
  - corrigido: `ApplyState` agora volta ao padrão os parâmetros que o preset não tem (preset salvo antes de o
    parâmetro existir herdava o valor do som anterior, ex.: `drive.type` = NAM); `ampcli -drive-nam` põe o drive na
    cadeia (`Chain.Add`) quando o preset não o tem
- [x] **Voz High Gain no amp simulado** (19/11, `dsp/highgain.go`, `dsp.TMBHighGain`): o seletor do cabeçote virou
  **Voz: Moderno | Americano | Britânico | High Gain** (parâmetro `pre.stack` = 3; os presets antigos não mudam).
  Motivo: o Metal com o Sat7 (pré do GT208, ceifamento por diodos) não tinha peso de grave (150–400 Hz a −26,6 dB
  do total); uma captura de um EVH 5150III Ivory, canal vermelho (`NAM/APP-5153-Ivory-Red-X.nam`, do TONE3000,
  fora do git e não distribuída), serviu de **referência de medição** para um modelo nosso
  - pré: passa-alta de entrada (~420 Hz) e shelf leve de brilho → a 4x, três estágios `tanh(−g·x + b) − tanh(b)`
    (a fase inverte a cada estágio, então a assimetria alterna como numa cascata de triodos), com passa-alta de
    300 Hz depois do 1º e passa-baixas de ~3 kHz depois do 1º e do 2º (seguram o chiado) → DC → nível. O knob de
    ganho muda o 1º estágio de 0,05× a ~506× (escala log; 8 = ponto do ajuste); os estágios 2 e 3 dão ~55 dB
  - tone stack: TMB com os pots do Marshall e slope/capacitores ajustados (R4 66k, C1 332p, C2 47n, C3 31n)
  - **ajuste** (`cmd/hgfit`, busca por coordenada contra a captura rodando no nosso `nam/`): espectro por 1/3 de
    oitava com a DI e com ruído em −30/−50 dBFS, THD de senos em 82–1760 Hz de −72 a −32 dBFS e compressão.
    Resultado: erro médio de 0,67 dB (DI) e 0,85 dB (ruído −30); a THD acompanha a da captura, inclusive o "tight"
    (82 Hz: 2,4→14% de −72 a −56 dBFS na captura, 2,6→14% no modelo). `Out` fixo em 0,25 (acima disso o power amp
    satura e o ajuste piora) e C2 limitado a 47 nF (com 200 nF o grave saturava no meio do knob)
  - a captura distorce forte mesmo em −72 dBFS (THD de 20 a 80% conforme a frequência): ganho de +55 a +72 dB em
    pequeno sinal, contra ~26 dB do pré clássico
  - engine: trocar de voz faz crossfade do pré inteiro quando o High Gain entra ou sai (entre as clássicas, só o
    tone stack muda, como antes); mesma latência (pico do impulso em 34 × 32 amostras); CPU ~2,7% em tempo real
    (blocos de 64), o dobro do pré clássico. Testes: troca ao vivo sem estalo e idêntica com blocos de 16/64/512/1024,
    zero alocação, estabilidade no knob todo, o knob de ganho muda a distorção
  - **Metal** passa a usar o amp simulado com a voz High Gain (ganho 8, tone stack em 5, boost modelado e o EQ do
    preset mantidos; volume −7,7 → −18,0 dBFS). Com a DI, fica a −2,3 a +2,5 dB do 5153 em todas as faixas; contra o
    Sat7, +3,6 dB em 60–150 Hz e +8,7 dB em 150–400 Hz
- [x] **Spillover na troca de preset** (19/11, `engine/spill.go`): antes, delay e reverb que continuavam na
  cadeia aplicavam os parâmetros novos sobre a cauda antiga (o tempo do delay deslizava 80 ms, desafinando os ecos)
  e os que saíam eram cortados na rampa da troca de ordem. Agora `ApplyState` avisa a thread de áudio, que passa a
  instância em uso de cada módulo com cauda (`Spiller`: delay e reverb) para uma cauda alimentada com silêncio, com os
  parâmetros antigos, e segue com uma reserva limpa (2 por módulo, criadas no `Prepare`; zero alocação). Caudas
  vizinhas formam um grupo em série (o eco antigo ainda passa pelo reverb antigo) e somam antes da próxima parte do
  amp (no loop, antes do power, então passam por power e gabinete). Fim: silêncio abaixo de −80 dB (`Silent`), ou
  fade de 1 s depois de 10 s; uma troca nova faz as caudas anteriores sumirem em 150 ms. Delay e reverb zerados
  aplicam o próximo ajuste na hora (sem rampa). Botão **Spillover** na pedaleira (salvo em `pedaleira.json`) e comando
  `spillover on|off` no console; desligado, a cauda some em 30 ms
- [x] **Estéreo nas ambiências e modulações** (19/11, `engine/stereo.go`): a entrada do pré é sempre mono; com o
  estéreo ligado, a cadeia tem L e R logo depois do pré-amp (sempre ali, não só a partir do 1º pedal estéreo:
  assim ligar um pedal não muda o trecho e não há estado do R a inventar). Cada módulo do trecho estéreo processa
  de um de três jeitos: `StereoProcessor` (delay, reverb e gabinete tratam os dois canais), `Twinner` (uma gêmea
  com os mesmos `*Param` no R: power, EQ, gate, compressor, vibrato, pitch; com `TwinSyncer` a gêmea recebe o LFO
  do L defasado em até 180° pela **Largura**, suavizado em 100 ms: chorus, flanger, phaser, vibe, tremolo →
  auto-pan, rotary → 2º microfone) ou soma L+R e processa em mono (drive, wah, octave, synth). Delay **Duplo** (o
  R até 25 ms mais atrasado) ou **Ping-pong** (L → R → L, cada repetição com o filtro do tone); reverb com um 2º
  conjunto de combs/all-pass +23 amostras (Freeverb), alimentado por (L+R)/2. Largura padrão 100%, menos no
  tremolo (0%: continua tremolo). Testes: estéreo desligado = mono bit a bit; estéreo com larguras em 0 = mono
  (cadeia completa); blocos de 64 e 1024 iguais; ping-pong e duplo no lugar certo; troca mono/estéreo ao vivo com
  a rampa da reordenação, sem alocar; spillover em estéreo. CPU: ~+2% do tempo real (power e gabinete dobrados;
  NAM + chorus/delay/reverb: 6,4% → 8,5% com blocos de 256 a 48 kHz).
  App: render L/R (placa de 1 canal recebe a média), A/B e mudo nos dois canais, analisador com (L+R)/2,
  gravação e exportação em 2 canais quando estéreo (`wav.CreateChannels`, `engine.RenderOfflineChannels`),
  `ampcli -estereo`, comando `estereo on|off` no console. Botão **Estéreo** ao lado de CADEIA DE SINAL (no
  rodapé não cabia na janela mínima), salvo em `~/.config/ampsim/ajustes.json` (`presets.Settings`; padrão:
  ligado); em mono a GUI esconde Largura e o modo estéreo do delay (`engine.StereoOnly`)
- [ ] **Estéreo: ouvir ao vivo** e decidir se algum preset de fábrica ganha ping-pong ou outra largura (hoje todos
  usam os padrões; os picos em estéreo seguem abaixo de −1 dBFS com a DI)
- [x] **Knob de grave dos TMB quase parado acima de 5** (19/11, `dsp/tmb.go`): o circuito satura acima de ~20% do
  pot, e a curva log antiga (Yeh e Smith) fazia ~80% do efeito de 0 a 5 (Americano: 5,5 dB em 100 Hz de 0 a 5 e 1,3
  de 5 a 10). Agora a curva sai do circuito de cada modelo (tabela de 41 pontos por bisseção, cache por modelo):
  passos iguais de dB em 100 Hz com médio e agudo em 5, do pot a 1% até o máximo (faixa de 10,3 dB no Americano,
  9,1 no Britânico, 8,3 no High Gain). O 5 novo é mais magro; o antigo 5 é ~8,7 (Americano), 9,0 (Britânico) e 9,3
  (High Gain). Formato do preset v4: presets v1–v3 têm o grave dos TMB convertido ao carregar (mesma posição do
  pot, mesmo som; o Moderno não muda); Surf, Ambiente e Metal remapeados; compensação de nível igual à de antes;
  `hgfit` ajusta com o pot na posição antiga. Testes: passos lineares (±0,05 dB), inverso, migração, presets
  de fábrica na posição antiga do pot
- [ ] **Drive NAM: ouvir ao vivo** com a guitarra (TS9/HOT na frente das capturas Dist e Clean do amp) e decidir:
  aviso na GUI quando drive e amp estão os dois no Completo; Blues e Worship mais escuros com o TS7 (compensar no EQ
  ou não); o pico de saída com o
  SD-1 cranked na frente do Clean V10 chega a +7,6 dBFS no volume padrão (o ganho do pedal agora é real: baixar o
  Level ou o volume)
- [x] **Wah: knobs do modo** (19/11, `engine.ParamViewer`): Envelope = Sensib., Resposta, Faixa, Q, Mix; LFO =
  Rate, Faixa, Q, Mix; Fixo = Posição, Faixa, Q, Mix. Teste: cada knob visível muda o som e os escondidos não
- [x] **Atalhos no Menu** (19/11, `cmd/ampapp/ui/shortcuts.go`): lista do teclado (T, A, 1 a 6, Espaço, Esc) e do
  mouse (Shift, duplo clique, roda, arrastar); teste confere que as teclas de `App.keys` estão na lista
- [x] **Backup e restauração dos presets** (19/11, `presets/backup.go`): `.zip` com `presets/*.json`,
  `pedaleira.json` e `arquivos/` (os `.nam`/IRs do usuário usados pelos presets; as incluídas ficam como
  referência). Restaurar valida o zip inteiro antes de gravar, guarda o estado atual em
  `~/.config/ampsim/backups/antes-de-restaurar-*.zip`, substitui os presets de mesmo nome e a pedaleira, e põe os
  arquivos em `~/.config/ampsim/arquivos/` (os presets passam a apontar para lá)
- [x] **Analisador de espectro** (19/11, `cmd/ampapp/audio/analyzer.go`, `cmd/ampapp/ui/spectrum.go`): a thread de
  áudio copia entrada (antes da cadeia) e saída para uma fila sem trava (sem alocar; teste); a GUI faz uma FFT de
  8192 pontos (as duas reais numa complexa), Hann, faixas de 1/6 de oitava (interpoladas nos graves), suavização
  rápida para subir e lenta para descer. Compacto na linha da ENTRADA (clique abre o grande); grande com Saída,
  Entrada, EQ e gabinete, Entrada alinhada (pela média de 100 Hz a 5 kHz) e Ligado, escalas e leitura sob o
  ponteiro. Janela minimizada: a fila enche e o áudio descarta (sem custo)
- [x] **Atrasos (callbacks fora do tempo) com a captura Sat7** (19/11, prints do teste ao vivo): CPU 52% e
  299–436 atrasos no Solo Hard Rock e no Metal, contra 12% e ~60 nos limpos, com buffer 256. Investigado (19/11):
  - **a Sat7 não é mais cara**: todas as capturas do GT208 têm a mesma rede; NAM sozinho, blocos de 256: ~4,6% no
    Lite e ~25% no Completo, igual no Clean V10 e na Sat7 MD. A troca do passado nos rings não dá pico periódico
  - os 52% da tela são a **maior** carga de um callback (não a média) e só aparecem com o amp no **Completo**: cadeia
    do Solo Hard Rock offline ~10% (p99 18%) no Lite e ~29% (p99 49%, máx 50%) no Completo. Os presets de fábrica
    gravam `nam_size` = Lite; no teste o Completo provavelmente estava ligado à mão
  - sem alocação no processamento (todos os presets, Lite e Completo, mono e estéreo)
  - profiler do Completo: 73% no `axpy4` (escalar) → **núcleos SIMD** (`nam/kernels_simd_amd64.go`, pacote
    experimental `simd/archsimd`, só com `GOEXPERIMENT=simd`; CPU sem AVX2/FMA ou build sem o experimento usa os
    escalares de `nam/kernels.go`). O `convRows` percorre a saída em pedaços de 32 amostras em 4 registradores,
    somando todos os pares (tap, entrada) com FMA: a saída é lida e gravada uma vez só (o `axpy4` vetorizado
    direto deu só 1,7×). O resto do bloco (< 8) vai por um vetor na pilha nas mesmas instruções (silêncio continua
    dando saída constante; a carga parcial do archsimd é acusada pelo checkptr do `-race`)
  - resultado (i5-10310U, blocos de 256): **modelo completo ~3× mais rápido** (33–37% → 11–12% do tempo real);
    cadeia do Solo Hard Rock no Completo 36% → 16% de média e 60% → 30% de pico; no Lite 10% → 7%. Contra a NAM
    core: SNR 95 dB (APP-5153 completo, antes 99,6) e 121 dB (Sat7), dentro da tolerância
  - `scripts/empacotar.sh` compila com `GOEXPERIMENT=simd` (avisa se o binário saiu sem); `ampsim -versao` mostra
    "NAM: AVX2 + FMA" ou "escalar"; teste `TestConvRowsIgualAoEscalar` (kernels, dilatação, resto, 1×1)
- [ ] **Atrasos: conferir ao vivo** com o build SIMD (Solo Hard Rock no Lite e no Completo, buffer 256 e 64). Se os
  atrasos dos limpos (~60 com 12% de carga) continuarem, eles vêm de agendamento/pausas do Go (ver D7: `GOGC`,
  prioridade da thread do JACK), não do DSP
- [ ] **Atraso sutil ao tocar** (19/11): conferir o botão **Mixer** da AudioBox todo em Playback (senão o seco
  direto soma com o processado ~30–40 ms depois e soa dobrado); o teste foi com buffer 256 (a medição de 31 ms foi
  com 64); aplicar o D1 (regra do WirePlumber, −9 ms)
- [ ] **High cut do gabinete nos presets NAM**: testar de ouvido ~5 kHz em vez de 6,2 kHz (as capturas são só do
  pré e soam brilhantes); se resolver, vira um ajuste só em vez de EQ em todos os presets
- [ ] **Controle por MIDI** (futuro; pedido em 18/11):
  - entrada MIDI pelo backend JACK próprio (porta MIDI do cliente `ampsim`) e, nos outros sistemas, pelo backend do
    miniaudio ou uma biblioteca de MIDI
  - **Program Change** → preset (os mesmos bancos/slots da pedaleira: PC 0–5 = banco 1, 6–11 = banco 2...)
  - **Control Change** → ligar/desligar pedais (CC ≥ 64 = liga) e parâmetros contínuos (volume, níveis, mix, drive,
    posição do wah por pedal de expressão), com **MIDI learn**: clicar com o botão direito num knob ou LED e mexer no
    controlador; mapeamento salvo em `~/.config/ampsim/midi.json`
  - **MIDI clock** → andamento do delay, tremolo e synth (como o tap tempo); nota/CC para o TAP
  - suavização dos CCs (7 bits = 128 passos: usar o Smoother dos parâmetros) e, quando houver, CC de 14 bits / NRPN
  - feedback para controladores com LED (mandar o estado de volta)
- [x] **Release v0.7.0** (18/11/2026): arrastar os pontos do EQ
- [x] **EQ: arrastar os pontos na curva** (18/11): cada banda tem um ponto em (frequência, ganho da banda);
  arrastar muda os dois (limitado à faixa da banda) e os knobs acompanham (leem os mesmos parâmetros); roda do mouse
  sobre o ponto muda o Q (só nos médios); duplo clique zera o ganho; o ponto ativo cresce e mostra "banda: freq ·
  ganho · Q"; cursor de mão. Teste com arrastar, limite da faixa, Q, duplo clique e clique fora
- [x] **Release v0.6.0** (18/11/2026): o pedal Synth
- [x] **Ganho na saída com NAM limpo**: com a DI de teste, a captura Clean V10 no volume padrão (−12 dB) tem pico de
  +1,8 dBFS (captura limpa tem fator de crista alto; a normalização por loudness a deixa ~1,5 dB acima do simulado);
  o Octave (seco 100% + −1 oit. 70%) soma +2 dB de pico. Avaliar: limitador de segurança na saída, alvo de loudness
  por tipo de captura, ou volume padrão mais baixo no modo NAM
  - **Feito (19/11): limitador de segurança** (`dsp.SafetyLimiter`, no fim da `Chain`): teto −0,3 dBFS, sem
    lookahead (latência zero), L e R com o mesmo ganho, ataque instantâneo, hold de 10 ms (o ganho não segue cada
    meio ciclo de uma nota grave) e volta de 50 ms; abaixo do teto a saída é bit a bit a entrada. A `Chain` começa
    com ele desligado (medições offline e `ampcli` sem ele; `ampcli -limitador`); o app liga pelo ajuste
    `limitador` de `ajustes.json` (padrão: ligado), botão **Limitador** ao lado de Estéreo, comando `limitador` no
    console; gravar e exportar passam por ele (`engine.Output`). O LED CLIP do rodapé vira **LIMIT** com ele ligado
    (âmbar quando reduz ≥ 0,1 dB; vermelho se ainda cortar). Com a DI e a Clean V10 no volume padrão (+1,8 dBFS de
    pico), reduz > 0,1 dB em ~11% do tempo e > 1 dB em ~4%: segura, mas o nível dessa captura continua alto
    (alvo de loudness por tipo de captura fica em aberto, se incomodar ao vivo)
- [x] **Synth (filtro de sintetizador)** (18/11, `dsp/synthfilter.go`): ladder de 4 polos não linear (Huovilainen,
  tanh racional em cada estágio e na realimentação com compensação de graves), a 4x; saídas LP, BP e HP; ressonância
  até a auto-oscilação. Corte = Cutoff · 2^(Profund.·mod), mod por Envelope, LFO ou Sequência de 8 passos (s1..s8,
  glide, rate em passos/s com tap tempo e subdivisões 1/4..1/16; inicial 1/16); FM na velocidade do áudio pelo próprio
  sinal normalizado pelo envelope (até ±2 oitavas, independe da palhetada). Drive de entrada, Mix (seco alinhado com a
  latência) e Level. GUI: editor dos passos (arrastar as barras, passo atual aceso), knobs em 2 linhas de 5, chaves
  de modo e de filtro, TAP (a tecla T também vale para ele). Nível na DI: −1,4 a −0,3 dB; CPU 2,4% com FM.
  Testes: LP/HP/BP no lugar, ressonância sem salto nos graves, sequência e envelope movendo o corte, estabilidade com
  FM e ressonância máximas, editor de passos na GUI. (Guitar synth com detecção de nota: descartado, latência e
  monofonia o tornam pouco viável)
- [x] **Release v0.5.0** (18/11/2026): EQ, tremolo, vibrato, adicionar/remover pedais e os 6 pedais novos; níveis
  conferidos com a DI (cada pedal no padrão fica entre −4 e +5 dB de RMS; o wah subiu 5 dB, estava em −8 dB)
- [x] **Tap tempo no tremolo**: botão TAP e subdivisões 1/4, 1/8 (inicial), 1/8T e 1/16 (ciclos por tempo); o
  `TapControl` serve a um parâmetro de tempo (delay, ms) ou de taxa (tremolo, Hz). Tecla T: com um pedal com TAP
  aberto, só nele; com nenhum aberto, em todos (andamento global)
- [x] **Nome AmpSim** padronizado no que o usuário vê (janela, topo, cabeçote, Sobre, `-versao`, menu, pacote,
  READMEs); identificadores técnicos continuam `ampsim` (módulo Go, executáveis, `~/.config/ampsim`, arquivos de
  gravação, cliente JACK)
- [x] **Release v0.4.0** (18/11/2026) com o NAM, as capturas e as IRs de fábrica (`.deb` e `.tar.gz`, conferidos
  fora da pasta do projeto)
- [x] GUI: seletor **Amp: Simulado | NAM** no cabeçote; no modo NAM, a seção mostra o botão do modelo (lista
  "MODELOS NAM" com as incluídas + "Abrir arquivo .nam…", filtro `*.nam` no zenity), Lite | Completo, canais e
  tamanho em uso, aviso de taxa diferente e o knob de entrada; o power mostra só o volume; o bloco PRÉ da cadeia
  vira "NAM". Conferido em captura de tela nos dois modos
- [x] `ampcli -nam "<captura>"` ou `-nam arquivo.nam` (reamostra a entrada para 48 kHz no modo NAM);
  `ampcli -listar` mostra as capturas incluídas
- [x] **Reamostragem em tempo real** (19/11): com a placa em outra taxa (ex.: 44,1 kHz), cada rede NAM (amp e Drive)
  roda dentro de uma `dsp.RateBridge`: polifásico racional em streaming (44,1→48→44,1 usa L/M = 160/147), FIR de
  fase linear com janela de Kaiser, mesmo protótipo na ida e na volta (mesma taxa alta)
  - banda útil até 0,41·min(fs, 48k) (18 kHz), rejeição a partir de 0,59·min (nada dobra para dentro da banda)
  - a latência da ida e volta é inteira e cabe nas 32 amostras que o bloco NAM já compensava (o que falta vira
    atraso): **a latência da cadeia não muda**. A 44,1 kHz: 28 amostras, 80 dB, 29/31 coeficientes por fase,
    ~0,2% de CPU (13,6 µs por bloco de 256). A 88,2/96 kHz as 32 amostras são só 0,33 ms: ~50 dB
  - a amostra j da volta fica pronta quando a amostra j da entrada chega, então não há FIFO com atraso extra e a
    saída não depende do tamanho do bloco (testado bit a bit com 1/37/64/500/512)
  - contra o caminho offline (`resample_poly` na DI inteira): erro −50 dB nas limpas e −44 dB nas distorcidas
    até 15 kHz (a diferença que sobra vem do que a rede recebe acima de 18 kHz); impulso alinhado com o simulado
  - a GUI mostra "44,1↔48 kHz" na linha de detalhes do modelo; o aviso de taxa só aparece se não der para
    reamostrar (taxa não inteira ou razão L/M > 4096). O `ampcli` continua reamostrando a entrada offline para 48 kHz
- [x] **Calibração de entrada por `input_level_dbu`** (19/11), com a regra do plugin oficial: ganho de entrada do
  modelo += nível da placa (dBu em 0 dBFS) − `input_level_dbu` da captura. Vale para o amp e o Drive NAM; modelos
  sem o metadado (todas as capturas incluídas) não mudam
  - `Chain.SetInputCalibration(on, dBu)` (atômico, rampa de 20 ms ao ligar, mudar o nível ou trocar de modelo);
    padrão desligada em +12 dBu (o padrão do plugin); limites −20..+40 dBu
  - é ajuste do equipamento, não do som: fica em ajustes.json (`calibrar_entrada`, `nivel_entrada_dbu`), fora do
    preset; a exportação leva junto (`engine.OutputOf`)
  - painel Áudio…: seção CALIBRAÇÃO DE ENTRADA (NAM) com Ligada/Desligada, nível com − / + (0,5 dB) e o ajuste
    em uso ("amp: captura a +13.0 dBu → entrada −1.0 dB"); console `calibrar [on|off|<dBu>]`; `ampcli -nivel-entrada`
  - testes: calibrada = −1 dB no knob Entrada (diferença < −100 dB); desligada e sem metadado não mudam nada;
    Drive; limites; zero alocação ligando e desligando; ajustes.json ida e volta; botões do painel
  - AudioBox USB: o manual não traz o nível máximo da entrada de instrumento, e o knob GAIN o muda dB por dB
    (só daria para medir com multímetro e o knob numa posição marcada). Decisão (19/11): não medir; fica
    desligada e o nível é acertado à mão com o trim + knob Entrada. Útil com placas de ganho fixo que publicam o valor
- [ ] Testar ao vivo com a guitarra (CPU no callback com o completo, crossfades, nível de entrada ideal)
- [ ] Medir a CPU com standard (A1, 16 canais) quando houver um arquivo (o SIMD de 19/11 já vale para ele)
- [ ] (Depois) LSTM

**Consequências para o projeto**
- **Sem knobs de amp:** a captura congela um ajuste do amp; trocar de som = trocar de arquivo. O tone stack atual pode
  servir de EQ antes/depois.
- **Loop de efeitos:** como a captura junta pré e power, o "loop" passa a ser entre o amp NAM e o gabinete.

**Custo aproximado (a conferir com arquivos reais):** o custo cresce com o quadrado dos canais.

| Tamanho | Canais (aprox.) | Custo relativo | Em Go puro (estimativa) |
|---|---|---|---|
| standard | 16 / 8 | 1× (~14 mil multiplicações por amostra) | pesado; provavelmente precisa otimizar |
| lite | 12 / 6 | ~0,5× | deve rodar bem |
| feather | 8 / 4 | ~0,25× | folgado |
| nano | 4 / 2 | mínimo | folgado |

## 7. Estimativa total (meio período)

| Marco | Acumulado |
|---|---|
| CLI validado contra o Python (Fase 1) | ✅ concluído |
| App tocando WAV ao vivo (Fase 2) | ✅ concluído |
| App com GUI (Fase 3) | ✅ concluído (v0.1.0) |
| **MVP:** tocando ao vivo (Fase 4) | ✅ concluído (18/11; latência 31 ms, débitos em 6.1) |
| Standalone completo (Fase 5) | ~9 semanas |
| Plugin LV2 no Audacity (Fase 6a) | ~11 semanas |
| Plugin CLAP no Reaper (Fase 6b) | +~1 semana |
| Suporte a NAM (pós-MVP) | ~1,5 a 2 semanas, quando for priorizado |

## 8. Riscos

| Risco | Mitigação |
|---|---|
| Estalos por GC ou alocação na thread de áudio | Teste de zero alocação; buffers pré-alocados; `GOGC` |
| Latência alta ao vivo | Buffers pequenos; oversampling de fase mínima; primeira partição da IR pequena |
| Diferença de nível entre a DI gravada e a guitarra ao vivo | Trim de entrada com medidor |
| Dois runtimes Go no mesmo host (plugin) | Um único `.so` para todos os efeitos |
| GUI do plugin dentro da janela da DAW | Plugin só aplica presets: UI genérica do host (Fase 6) |
| NAM standard pesado demais em Go puro | Validar com lite/feather primeiro; otimizar; em último caso, cgo com a NAM core |

## 9. Referência Python (`referencia_python/`)

Usar com o venv da raiz: `venv/bin/python referencia_python/main.py`

| Arquivo | Conteúdo | Observação |
|---|---|---|
| `main.py` | Gate → saturação com oversampling → gate → IR | Lê `testdata/` e grava em `referencia_python/saida/` |
| `chorus.py`, `flanger.py`, `phaser.py`, `delay.py`, `reverb.py` | Efeitos de modulação, delay e reverb | O exemplo do fim (que lê um WAV em `~/Downloads/...`) só roda com `python efeito.py`; importar não executa nada |
| `gerar_referencias.py` | Gera as saídas de referência para os testes Go | Grava em `testdata/referencia/`; parâmetros iguais aos de `dsp/referencia_test.go` |

**Bug corrigido no `main.py` (17/11/2026):** `np.mean` nos canais convertia int16 para float64 antes do teste de tipo
inteiro, então a normalização nunca rodava e o áudio entrava em ±32768. Os gates nunca fechavam e a saturação
amplificava o chiado até volume cheio no fim das notas. Além disso, a DI termina com um clique (~-20 dBFS nos últimos
10 ms), tratado com um fade de 30 ms na entrada. **A leitura de WAV em Go precisa normalizar antes de fazer a média
dos canais.**
