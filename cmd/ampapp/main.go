// ampapp é o app standalone do ampsim.
//
// Fase 2: console interativo tocando um WAV em loop pelo amp, em tempo real.
// A GUI (Gio) entra na Fase 3 usando os mesmos pacotes audio/ e source/.
//
//	go run ./cmd/ampapp                       # toca testdata/guitarra_di.wav em loop
//	go run ./cmd/ampapp -arquivo minha_di.wav -buffer 128
//	go run ./cmd/ampapp -listar-dispositivos
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"ampsim/cmd/ampapp/audio"
	"ampsim/cmd/ampapp/source"
	"ampsim/engine"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		arquivo  = flag.String("arquivo", "testdata/guitarra_di.wav", "WAV para tocar")
		ir       = flag.String("ir", "testdata/gabinete_2x12_sm57.wav", "IR do gabinete (vazio = sem gabinete)")
		preset   = flag.String("preset", "", "preset JSON para carregar")
		disp     = flag.Int("dispositivo", -1, "índice do dispositivo de saída (-1 = padrão)")
		taxa     = flag.Uint("taxa", 48000, "taxa de amostragem")
		buffer   = flag.Uint("buffer", 256, "tamanho do buffer em quadros")
		listar   = flag.Bool("listar-dispositivos", false, "lista as saídas de áudio e sai")
		tocar    = flag.Bool("tocar", true, "começa tocando")
		semLoop  = flag.Bool("sem-loop", false, "toca uma vez só")
		comandos = flag.String("comandos", "", "comandos iniciais separados por ';' (ex.: \"set pre.gain=8; on delay\")")
	)
	flag.Parse()

	if *listar {
		devs, err := audio.ListOutputs()
		if err != nil {
			return err
		}
		for _, d := range devs {
			def := ""
			if d.IsDefault {
				def = "  (padrão)"
			}
			fmt.Printf("%2d  %s%s\n", d.Index, d.Name, def)
		}
		return nil
	}

	chain := engine.NewDefaultChain()
	if *ir != "" {
		if err := chain.Cab().LoadIRFile(*ir); err != nil {
			return err
		}
	}
	if *preset != "" {
		if err := chain.LoadState(*preset); err != nil {
			return err
		}
	}

	out, err := audio.OpenOutput(audio.Config{
		Device: *disp, SampleRate: uint32(*taxa), Buffer: uint32(*buffer),
	}, chain)
	if err != nil {
		return err
	}
	defer out.Close()

	player := source.NewFilePlayer(out.SampleRate())
	player.SetLoop(!*semLoop)
	if *arquivo != "" {
		if err := player.Load(*arquivo); err != nil {
			return err
		}
	}
	out.Renderer().SetSource(player)

	mon := newMonitor(out.Renderer())
	go mon.run()

	if err := out.Start(); err != nil {
		return err
	}
	if *tocar {
		player.Play()
	}

	a := &app{chain: chain, out: out, player: player, mon: mon}
	fmt.Printf("ampsim — saída: %s, %.0f Hz, buffer %d (latência estimada %.1f ms)\n",
		out.Name(), out.SampleRate(), out.Buffer(), out.OutputLatencyMs(chain.Latency()))
	a.status()
	fmt.Println(`digite "ajuda" para ver os comandos`)

	for _, c := range strings.Split(*comandos, ";") {
		if c = strings.TrimSpace(c); c != "" {
			fmt.Println(">", c)
			if a.exec(c) {
				return nil
			}
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	for {
		fmt.Print("> ")
		select {
		case <-sig:
			fmt.Println()
			return nil
		case l, ok := <-lines:
			if !ok {
				return nil
			}
			if a.exec(l) {
				return nil
			}
		}
	}
}

// ---------------------------------------------------------------- medidores

// monitor consome a fila de medidores e acumula picos entre leituras.
type monitor struct {
	r *audio.Renderer

	mu       sync.Mutex
	inPeak   float32
	outPeak  float32
	clips    uint64
	maxLoad  float32
	gateOpen bool
}

func newMonitor(r *audio.Renderer) *monitor { return &monitor{r: r} }

func (m *monitor) run() {
	t := time.NewTicker(20 * time.Millisecond)
	for range t.C {
		m.mu.Lock()
		for {
			v, ok := m.r.Meters().Pop()
			if !ok {
				break
			}
			m.inPeak = max(m.inPeak, v.InPeak)
			m.outPeak = max(m.outPeak, v.OutPeak)
			m.clips += uint64(v.OutClips)
			m.maxLoad = max(m.maxLoad, v.Load)
			m.gateOpen = v.GateOpen
		}
		m.mu.Unlock()
	}
}

// take devolve os valores acumulados e zera os picos.
func (m *monitor) take() (in, out float32, clips uint64, load float32, gate bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, out, clips, load, gate = m.inPeak, m.outPeak, m.clips, m.maxLoad, m.gateOpen
	m.inPeak, m.outPeak, m.maxLoad = 0, 0, 0
	return
}

func db(v float32) float64 { return 20 * math.Log10(float64(v)+1e-9) }

// barra de -60 a 0 dBFS
func bar(v float32) string {
	const n = 20
	k := int((db(v) + 60) / 60 * n)
	k = max(0, min(n, k))
	return strings.Repeat("█", k) + strings.Repeat("·", n-k)
}

// ---------------------------------------------------------------- comandos

type app struct {
	chain  *engine.Chain
	out    *audio.Output
	player *source.FilePlayer
	mon    *monitor
}

const ajuda = `comandos:
  play | pause | stop            reprodução
  seek <s>                       pula para a posição em segundos
  loop on|off                    liga/desliga o loop
  loop <início> <fim>            trecho do loop em segundos (fim 0 = até o fim)
  abrir <arquivo.wav>            troca o arquivo
  ir <arquivo.wav>               troca a IR do gabinete (ao vivo)
  set modulo.param=valor ...     ex.: set pre.gain=8 pre.treble=6 power.volume=-9
  on <módulos> | off <módulos>   liga/desliga pedais, ex.: on chorus delay
  ordem                          mostra a cadeia (com o loop de efeitos)
  ordem <id> <id> ...            define a ordem completa
  mover <id> antes|depois <ref>  ex.: mover delay antes power (loop) / mover delay depois cab
  listar                         módulos, parâmetros e valores
  status                         estado e medidores
  medir [segundos]               medidores ao vivo (padrão 5 s)
  salvar <arq.json> | carregar <arq.json>   presets
  sair`

// exec executa um comando; devolve true para sair.
func (a *app) exec(line string) bool {
	f := strings.Fields(line)
	if len(f) == 0 {
		return false
	}
	cmd, args := strings.ToLower(f[0]), f[1:]
	rest := strings.Join(args, " ")
	var err error

	switch cmd {
	case "sair", "q", "quit", "exit":
		return true
	case "ajuda", "help", "?":
		fmt.Println(ajuda)
	case "play", "p":
		a.player.Play()
	case "pause":
		a.player.Pause()
	case "stop":
		a.player.Stop()
	case "seek":
		var s float64
		if s, err = arg(args, 0); err == nil {
			a.player.Seek(s)
		}
	case "loop":
		switch {
		case len(args) == 1 && args[0] == "on":
			a.player.SetLoop(true)
		case len(args) == 1 && args[0] == "off":
			a.player.SetLoop(false)
		case len(args) == 2:
			var s, e float64
			if s, err = arg(args, 0); err == nil {
				if e, err = arg(args, 1); err == nil {
					err = a.player.SetLoopRegion(s, e)
					a.player.SetLoop(true)
				}
			}
		default:
			err = fmt.Errorf("use: loop on|off ou loop <início> <fim>")
		}
	case "abrir":
		err = a.player.Load(rest)
	case "ir":
		err = a.chain.Cab().LoadIRFile(rest)
	case "set":
		err = a.chain.SetList(rest)
	case "on":
		err = a.chain.EnableList(rest, true)
	case "off":
		err = a.chain.EnableList(rest, false)
	case "ordem":
		if len(args) > 0 {
			err = a.chain.SetOrder(engine.SplitList(rest))
		}
		if err == nil {
			fmt.Println("cadeia:", a.chain)
		}
	case "mover":
		if len(args) != 3 || (args[1] != "antes" && args[1] != "depois") {
			err = fmt.Errorf("use: mover <id> antes|depois <ref>")
		} else if args[1] == "antes" {
			err = a.chain.MoveBefore(args[0], args[2])
		} else {
			err = a.chain.MoveAfter(args[0], args[2])
		}
		if err == nil {
			fmt.Println("cadeia:", a.chain)
		}
	case "listar":
		a.listar()
	case "status", "s":
		a.status()
	case "medir":
		secs := 5.0
		if len(args) > 0 {
			secs, err = arg(args, 0)
		}
		if err == nil {
			a.medir(secs)
		}
	case "salvar":
		err = a.chain.SaveState(rest)
	case "carregar":
		err = a.chain.LoadState(rest)
	default:
		err = fmt.Errorf("comando desconhecido %q (digite ajuda)", cmd)
	}
	if err != nil {
		fmt.Println("erro:", err)
	}
	return false
}

func arg(args []string, i int) (float64, error) {
	if i >= len(args) {
		return 0, fmt.Errorf("faltou o valor")
	}
	return strconv.ParseFloat(strings.ReplaceAll(args[i], ",", "."), 64)
}

func (a *app) status() {
	c := a.player.Clip()
	estado := "pausado"
	if a.player.Playing() {
		estado = "tocando"
	}
	if c != nil {
		ls, le := a.player.LoopRegion()
		loop := "loop desligado"
		if a.player.Loop() {
			loop = fmt.Sprintf("loop %.2f–%.2f s", ls, le)
		}
		fmt.Printf("arquivo: %s  %.2f/%.2f s  %s, %s\n", c.Name, a.player.Position(), c.Duration(), estado, loop)
	}
	fmt.Printf("cadeia: %s\n", a.chain)
	in, out, clips, load, gate := a.mon.take()
	g := "fechado"
	if gate {
		g = "aberto"
	}
	fmt.Printf("entrada %6.1f dBFS  saída %6.1f dBFS  clips %d  gate %s  CPU máx %.1f%%  callbacks %d\n",
		db(in), db(out), clips, g, 100*load, a.out.Renderer().Callbacks())
}

func (a *app) medir(secs float64) {
	a.mon.take()
	end := time.Now().Add(time.Duration(secs * float64(time.Second)))
	for time.Now().Before(end) {
		time.Sleep(200 * time.Millisecond)
		in, out, clips, load, gate := a.mon.take()
		g := "·"
		if gate {
			g = "G"
		}
		fmt.Printf("%5.2f s  in %s %6.1f  out %s %6.1f  %s  cpu %4.1f%%  clips %d\n",
			a.player.Position(), bar(in), db(in), bar(out), db(out), g, 100*load, clips)
	}
}

func (a *app) listar() {
	fmt.Println("cadeia:", a.chain)
	for _, m := range a.chain.Modules() {
		estado := "off"
		if a.chain.Enabled(m.ID()) {
			estado = "ON"
		}
		fmt.Printf("[%s] %s\n", estado, m.ID())
		for _, p := range m.Params() {
			in := p.Info()
			fmt.Printf("      %-10s %8.2f %-4s [%g .. %g]\n", in.ID, p.Get(), in.Unit, in.Min, in.Max)
		}
	}
}
