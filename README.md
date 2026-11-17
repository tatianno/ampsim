# ampsim

Simulador de amplificador de guitarra e pedais em Go, feito como estudo de DSP:
pré-amp com saturação sobreamostrada, tone stack, power amp, gabinete por resposta ao
impulso (IR) e pedais de gate, chorus, flanger, phaser, delay e reverb — tocando em
tempo real, com GUI.

Os algoritmos começaram como protótipos em Python (`referencia_python/`) e foram portados
para Go em streaming, validados contra as saídas do Python.

## O que tem

- **Amp em três partes** — pré-amp (ganho, grave, médio, agudo), power amp (presença,
  volume) e gabinete (IR, low cut, high cut).
- **Cadeia configurável** com **loop de efeitos** (send/return): os pedais podem ficar na
  frente do amp, entre o pré-amp e o power amp, ou depois do gabinete. A ordem muda ao vivo,
  sem estalo.
- **Tempo real**: convolução particionada de latência zero, oversampling 4x com latência de
  32 amostras, nenhuma alocação na thread de áudio, saída idêntica com qualquer tamanho de bloco.
- **GUI (Gio)**: cabeçote e gabinete com knobs, cadeia em que se arrastam os pedais, pedais
  que abrem por cima da tela, transporte com loop de trecho, medidores e bypass A/B.
- **CLI** para processar arquivos offline e **presets em JSON** (o mesmo formato servirá para
  o futuro plugin LV2).

## Requisitos

- Go 1.26+ e um compilador C (o áudio usa [malgo](https://github.com/gen2brain/malgo) / miniaudio via cgo)
- Para a janela (Gio), no Ubuntu/Debian:
  ```
  sudo apt install libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev \
                   libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev
  ```
  Sem essas bibliotecas, compile só o console com `-tags nogui`.
- Opcional: `zenity` (diálogo de abrir arquivo na GUI)

## Instalação

```sh
go install github.com/tatianno/ampsim/cmd/ampcli@latest   # CLI (offline)
go install github.com/tatianno/ampsim/cmd/ampapp@latest   # app em tempo real (precisa das bibliotecas acima)
go install -tags nogui github.com/tatianno/ampsim/cmd/ampapp@latest   # app só console
```

Ou clone o repositório e use `go run`, como abaixo.

## Uso

```sh
# GUI: toca um WAV em loop pelo amp (abre pausada: Play ou barra de espaço)
go run ./cmd/ampapp -arquivo minha_di.wav -ir meu_gabinete.wav

# console interativo (comandos: play, set pre.gain=8, on delay, mover delay depois cab, ab, ...)
go run ./cmd/ampapp -console -arquivo minha_di.wav -ir meu_gabinete.wav

# processar um arquivo offline
go run ./cmd/ampcli -in minha_di.wav -ir meu_gabinete.wav -out saida.wav -on delay -set pre.gain=8
go run ./cmd/ampcli -listar          # módulos e parâmetros
```

Grave a guitarra direto na interface (DI), sem amp nem pedal, com pico entre −12 e −6 dBFS.
A IR pode ser qualquer WAV de resposta ao impulso de gabinete (qualquer taxa; é reamostrada).

## testdata

Os arquivos de áudio de teste **não estão no repositório**. Para rodar os testes completos e
usar os caminhos padrão dos comandos, coloque em `testdata/`:

| Arquivo | Conteúdo |
|---|---|
| `testdata/guitarra_di.wav` | uma gravação DI de guitarra (alguns segundos) |
| `testdata/gabinete_2x12_sm57.wav` | uma IR de gabinete |

e gere as referências do Python (usadas na comparação Go × Python):

```sh
python3 -m venv venv && venv/bin/pip install numpy scipy
venv/bin/python referencia_python/gerar_referencias.py   # grava em testdata/referencia/
```

Sem esses arquivos, os testes que dependem deles são pulados e o resto roda normalmente.

## Testes

```sh
go test ./...          # sem placa de som e sem janela (a GUI é testada simulando mouse e teclado)
go test -race ./...
```

## Estrutura

```
wav/                leitura e gravação de WAV
dsp/                filtros, saturação, oversampling, convolução, efeitos (Go puro)
engine/             cadeia, parâmetros, presets
cmd/ampcli/         processamento offline
cmd/ampapp/         app em tempo real: audio/ (malgo), source/ (player), ui/ (Gio)
referencia_python/  protótipos originais e gerador de referências
```

O plano de desenvolvimento, decisões e resultados medidos estão em [PLANO.md](PLANO.md).

## Licença

[MIT](LICENSE). Os arquivos de áudio usados nos testes não fazem parte do repositório nem desta licença.
