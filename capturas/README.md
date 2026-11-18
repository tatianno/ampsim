# Capturas NAM incluídas

Modelos [NAM](https://github.com/sdatkinson/neural-amp-modeler) embutidos no ampsim (via `go:embed`), escolhidos no
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

- **Autor das capturas:** tatianno (autor do ampsim)
- **Treino:** TONE3000 (NAM 0.7). Cada arquivo é um `SlimmableContainer` com um modelo **lite** (3 canais, usado por
  padrão) e um **completo** (8 canais), a 48 kHz
- **Licença:** a mesma do projeto (MIT, veja `LICENSE` na raiz)

## IR do gabinete

`gabinete_2x12_sm57.wav` é a IR padrão do gabinete (um 2x12 com SM57), usada quando nenhuma IR é escolhida; também
vem embutida. Mesma licença (MIT).

Para incluir outra captura, basta colocar o `.nam` nesta pasta: o teste `TestTodasCarregam` confere se ela carrega.
