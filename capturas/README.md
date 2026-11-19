# Capturas NAM incluídas

Modelos [NAM](https://github.com/sdatkinson/neural-amp-modeler) embutidos no AmpSim (via `go:embed`), escolhidos no
cabeçote com **Amp: NAM → Modelo**. No `ampcli`: `-nam "<nome>"`; `ampcli -listar` mostra os nomes.

Uma coleção de capturas do pré-amp do **Warm Music GT208 (Ragna-Rock 208)**, um combo híbrido brasileiro de 60 W,
2x8. Cobre os canais limpo e de distorção em vários ajustes de ganho, todos com o EQ no meio (5).

**Só o pré-amp:** gravadas direto da saída do pré, antes do power amp e do alto-falante. Não incluem gabinete: no app
elas passam pelo gabinete por IR (use a IR que preferir).

## Sobre o amp

O GT208 tem um pré-amp híbrido: uma entrada transistorizada, com um canal de distorção por diodos ceifadores e o
tone stack, seguida de um único estágio valvulado na saída. Como o tone stack fica **depois** do ceifamento, ajustes
finos de EQ podem ser feitos, em boa parte, depois do modelo, com um EQ comum. O que muda de verdade a textura são os
controles de ganho:

- **Saturation:** o quanto o sinal bate nos diodos ceifadores (canal de distorção)
- **More Dist:** ganho extra no canal de distorção
- **Gain (chave):** ganho extra no estágio de entrada
- **Volume (limpo) / Level (distorção):** o nível enviado ao estágio valvulado; mais alto empurra mais a válvula

## Nomes

Os nomes mostram os ajustes usados:

| Parte | Significado |
|---|---|
| `Clean` / `Dist` | canal |
| `Sat` | Saturation |
| `V` | Volume (limpo) ou Level (distorção) |
| `MD` | More Dist ligado |
| `G` | chave Gain ligada |
| `B` / `M` / `T` / `P` | Bass / Middle / Treble / Presence |

Exemplo: `Dist Sat5 V4 MD B5 M5 T5 P5`.

O reverb estava desligado em todas. Testadas com um Seymour Duncan JB (TB-4) na ponte, um humbucker de saída alta;
com captadores mais fracos, espere uma resposta um pouco mais limpa.

## Ficha técnica

- **Autor das capturas:** tatianno (autor do AmpSim)
- **Treino:** TONE3000 (NAM 0.7). Cada arquivo é um `SlimmableContainer` com um modelo **lite** (3 canais, usado por
  padrão) e um **completo** (8 canais), a 48 kHz
- **Licença:** a mesma do projeto (MIT, veja `LICENSE` na raiz)

## Capturas de pedal (`pedais/`)

Usadas pelo **Drive** no **Tipo: NAM** (botão "CAPTURA NAM" no pedal); no `ampcli`: `-drive-nam "<nome>"`. São
**só o pedal**, sem amp nem gabinete: vão na frente de um amp (simulado ou NAM). Direto num gabinete soam ásperas,
como qualquer overdrive sem amp. A saída **não é normalizada** no app: o ganho do pedal (quanto ele empurra o amp)
vem da captura, e o knob Level atua depois do modelo.

**Ibanez TS7 Tone-Lok Tube Screamer**, nos modos padrão (estilo TS9) e HOT. Cada captura é um ajuste fixo, de um
quase unitário com o drive no zero ao drive no máximo. O Level ficou em 5 em todas, para a saída ser coerente entre
os ajustes. Bom para apertar um amp com crunch, como boost com médios na frente para solos, ou como overdrive sozinho
num amp limpo.

**Oliver SD-10 Super Over Drive**, a versão brasileira licenciada pela Roland do Boss SD-1, com Drive 10, Tone 6 e
Level 10: o circuito do SD-1 todo aberto. O ceifamento assimétrico (dois diodos de um lado da onda e um do outro)
acrescenta harmônicos pares, com uma quebra mais parecida com a de amp e textura um pouco granulada. Voz nos médios,
mas com mais graves que o Tube Screamer; com Drive e Level no máximo, satura bastante e sai quente, empurrando forte
o amp seguinte.

| Parte | Significado |
|---|---|
| `TS7 TS9` / `TS7 HOT` | modo do TS7 |
| `D` / `T` / `L` | Drive / Tone / Level (0–10) |
| entre parênteses | descrição do ajuste |

Os nomes originais usam um travessão (`TS7 TS9 – D5 T5 L5`), que o `go:embed` não aceita; aqui ficou um espaço.
Mesma ficha técnica das capturas de amp (TONE3000, NAM 0.7, lite + completo, 48 kHz) e mesma licença (MIT).

## IRs de gabinete

Também embutidas, escolhidas no gabinete (**IR: ▾**) ou com `-ir "<nome>"`; o nome sai do arquivo
(`gabinete_<falantes>_<microfone>.wav`):

| Arquivo | Nome | |
|---|---|---|
| `gabinete_1x10_sm58.wav` | 1x10 SM58 | 44,1 kHz, 53 ms |
| `gabinete_2x12_sm57.wav` | 2x12 SM57 | 48 kHz, 500 ms; **padrão** quando nenhuma IR é escolhida |
| `gabinete_4x12_sm7.wav` | 4x12 SM7 | 48 kHz, 16 ms |

Mesma licença (MIT). Para incluir outra IR, basta colocar o WAV aqui seguindo o padrão do nome.

Para incluir outra captura, basta colocar o `.nam` nesta pasta: o teste `TestTodasCarregam` confere se ela carrega.
