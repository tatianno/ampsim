# Simulador de Amplificador e Pedais em Go — Plano de Ação

> Aprovado em 17/11/2026. Este documento é a referência do projeto: decisões, arquitetura e lista de tarefas.
> Marque as caixas (`[x]`) conforme as tarefas forem concluídas.

## 1. Objetivo

Portar para Go os estudos de modulação e simulação de amplificador feitos em Python, chegando a:

1. **Aplicação standalone** com GUI: amp (Ganho, Grave, Médio, Agudo, Presença, Volume), gabinete por IR e pedais
   (Gate, Chorus, Flanger, Phaser, Delay, Reverb), tocando ao vivo pela guitarra ou a partir de um arquivo WAV.
2. **Plugin** (LV2/LADSPA, para Audacity e DAWs Linux) num segundo momento, reaproveitando o mesmo núcleo de DSP.

## 2. Decisões tomadas

| Tema | Decisão | Motivo |
|---|---|---|
| Linguagem | Go (1.26 instalado) | Escolha do projeto |
| Ordem | Standalone primeiro, plugin depois | Mais fácil de depurar; o núcleo é reaproveitado |
| Áudio I/O | `gen2brain/malgo` (miniaudio) | Full-duplex, multiplataforma, C embutido |
| GUI | Gio (`gioui.org`) | Go puro, bom para desenhar knobs e medidores |
| Pedais na GUI | Sobreposição (overlay) dentro do app | Simples e portátil; janelas do SO podem vir depois |
| Plugin | LV2/LADSPA com UI genérica do host | Audacity suporta; GUI própria no plugin é opcional |
| Plugin: binário | Um único `.so` com todos os efeitos | Só um runtime Go por processo |
| Tone stack | Começa com biquads (RBJ); depois modelo Fender/Marshall | Funciona rápido; o modelo analógico é um ótimo estudo |
| FFT (IR) | FFT radix-2 própria em `dsp/fft.go` (antes: gonum) | Mantém `dsp/` sem dependências; é pequena e testada contra a DFT |
| Módulo Go | `ampsim` | Definido no início da Fase 1 |
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
├── go.mod            # módulo ampsim
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

### Fase 3: GUI (janela funcionando; falta o teste de uso por você)
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
- [ ] Teste de uso de verdade (arrastar knobs e pedais com o som tocando) e ajustes que aparecerem

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

### Fase 4: Entrada ao vivo pela guitarra (~1 semana)
**Meta:** tocar ao vivo com latência total abaixo de ~10 ms.

- [ ] `source/LiveInput`: full-duplex com malgo, escolha do dispositivo e do canal
- [ ] Trim de entrada com indicador de clip (calibrar o nível ao vivo para bater com o da DI)
- [ ] Buffers de 64 a 128 amostras; testar com PipeWire/JACK
- [ ] Medir a latência real (loopback) e exibir na GUI
- [ ] Se houver estalos: revisar alocações e ajustar `GOGC`

### Fase 5: Recursos extras do standalone (~1 a 2 semanas)
- [ ] Presets: salvar, carregar e alguns de fábrica (Clean, Crunch, Lead)
- [ ] Gravar a saída em WAV (ao vivo ou arquivo)
- [ ] Exportar arquivo processado (offline, mesmo engine do `ampcli`)
- [ ] Tap tempo no Delay
- [ ] Afinador
- [ ] Tone stack com modelo Fender/Marshall (estudo; análise de D. Yeh, transformada bilinear)
- [ ] Otimização: FFT real na convolução e `tanh` aproximada no pré-amp (medir antes e depois)
- [ ] Empacotamento Linux (e depois Windows e macOS)

### Fase 6: Plugin (~1 a 2 semanas)
**Meta:** o amp e os pedais carregando no Audacity, com sliders genéricos.

- [ ] `plugin/lv2`: build `-buildmode=c-shared` + camada C com `//export`
- [ ] Um único `.so` com todos os plugins (amp + 6 efeitos)
- [ ] Gerar os `.ttl` (LV2) a partir da tabela `ParamInfo`
- [ ] Informar a latência ao host; estado via JSON do engine
- [ ] Testar no Audacity e no Ardour/Carla
- [ ] (Opcional) LADSPA, que é mais simples, sem seletor de IR
- [ ] (Opcional, longo prazo) GUI própria no plugin; CLAP ou VST3

## 7. Estimativa total (meio período)

| Marco | Acumulado |
|---|---|
| CLI validado contra o Python (Fase 1) | ✅ concluído |
| App tocando WAV ao vivo (Fase 2) | ✅ concluído |
| App com GUI (Fase 3) | ~3 semanas a partir daqui |
| Tocando ao vivo (Fase 4) | ~7 semanas |
| Standalone completo (Fase 5) | ~9 semanas |
| Plugin LV2 no Audacity (Fase 6) | ~11 semanas |

## 8. Riscos

| Risco | Mitigação |
|---|---|
| Estalos por GC ou alocação na thread de áudio | Teste de zero alocação; buffers pré-alocados; `GOGC` |
| Latência alta ao vivo | Buffers pequenos; oversampling de fase mínima; primeira partição da IR pequena |
| Diferença de nível entre a DI gravada e a guitarra ao vivo | Trim de entrada com medidor |
| Dois runtimes Go no mesmo host (plugin) | Um único `.so` para todos os efeitos |
| GUI do plugin dentro da janela da DAW | Começar com a UI genérica do host |

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
