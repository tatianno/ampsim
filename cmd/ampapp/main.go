// ampapp é o app standalone do ampsim: GUI (Gio) ou console, tocando um WAV pelo amp
// em tempo real.
//
//	go run ./cmd/ampapp                       # abre a janela com testdata/guitarra_di.wav
//	go run ./cmd/ampapp -console              # console interativo
//	go run ./cmd/ampapp -arquivo minha_di.wav -buffer 128
//	go run ./cmd/ampapp -listar-dispositivos
//	go build -tags nogui ./cmd/ampapp         # só console (sem as bibliotecas gráficas do sistema)
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
	"time"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
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
		preset   = flag.String("preset", "", "preset para carregar: arquivo .json ou nome (ex.: Lead)")
		saida    = flag.Int("saida", -1, "índice da saída de áudio (-1 = padrão; veja -listar-dispositivos)")
		entrada  = flag.Int("entrada", -1, "índice da entrada (-1 = padrão, -2 = sem entrada)")
		canal    = flag.Int("canal", 1, "canal da entrada onde está a guitarra (1, 2, ...; 0 = média dos canais)")
		fonte    = flag.String("fonte", "arquivo", "fonte inicial: arquivo ou guitarra")
		backend  = flag.String("backend", "", "backend de áudio: jack, pulseaudio ou alsa (vazio = automático: JACK próprio se houver)")
		taxa     = flag.Uint("taxa", 48000, "taxa de amostragem")
		buffer   = flag.Uint("buffer", 256, "tamanho do buffer em quadros (64, 128, 256...)")
		listar   = flag.Bool("listar-dispositivos", false, "lista as saídas e entradas de áudio e sai")
		tocar    = flag.Bool("tocar", false, "começa tocando o arquivo (padrão: sim no console, não na janela)")
		console  = flag.Bool("console", false, "usa o console em vez da janela")
		semLoop  = flag.Bool("sem-loop", false, "toca o arquivo uma vez só")
		comandos = flag.String("comandos", "", "comandos iniciais separados por ';' (ex.: \"set pre.gain=8; on delay\")")
	)
	flag.Parse()

	if *listar {
		return listarDispositivos(*backend)
	}

	chain := engine.NewDefaultChain()
	if *ir != "" {
		if err := chain.Cab().LoadIRFile(*ir); err != nil {
			return err
		}
	}
	if *preset != "" {
		st, err := presets.Resolve(*preset)
		if err != nil {
			return err
		}
		if err := chain.ApplyState(st); err != nil {
			return err
		}
	}

	s := audio.NewSession(chain, float64(*taxa))
	defer s.Close()
	s.Player.SetLoop(!*semLoop)
	if *arquivo != "" {
		if err := s.Player.Load(*arquivo); err != nil {
			return err
		}
	}
	setCanal(s.Live, *canal)
	switch *fonte {
	case "arquivo":
	case "guitarra":
		s.SetSource(audio.SourceGuitar)
	default:
		return fmt.Errorf("-fonte: use arquivo ou guitarra, veio %q", *fonte)
	}

	if err := s.Host.Open(audio.Config{
		Backend: *backend, Output: *saida, Input: *entrada, Buffer: uint32(*buffer),
	}); err != nil {
		return err
	}
	if w := s.Host.State().Warning; w != "" {
		fmt.Fprintln(os.Stderr, "aviso:", w)
	}
	tocarSet := false
	flag.Visit(func(f *flag.Flag) { tocarSet = tocarSet || f.Name == "tocar" })
	if *tocar || (*console && !tocarSet) {
		s.Player.Play()
	}

	if !*console {
		// A janela assume o processo (app.Main não retorna); ao fechar, libera o áudio e sai.
		return runGUI(s, s.Close)
	}

	a := &app{s: s, chain: chain, player: s.Player}
	a.info()
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

// setCanal converte o canal do usuário (1, 2, ...; 0 = média) para o da LiveInput.
func setCanal(l *source.LiveInput, c int) {
	if c <= 0 {
		l.SetChannel(source.ChannelMix)
	} else {
		l.SetChannel(c - 1)
	}
}

func listarDispositivos(backend string) error {
	outs, ins, err := audio.ListDevices(backend)
	if err != nil {
		return err
	}
	show := func(title string, ds []audio.DeviceInfo) {
		fmt.Println(title)
		for _, d := range ds {
			def := ""
			if d.IsDefault {
				def = "  (padrão)"
			}
			fmt.Printf("  %2d  %s%s\n", d.Index, d.Name, def)
		}
	}
	show("saídas (-saida):", outs)
	show("entradas (-entrada):", ins)
	return nil
}

// ---------------------------------------------------------------- medidores

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
	s      *audio.Session
	chain  *engine.Chain
	player *source.FilePlayer
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
  ab [on|off]                    bypass A/B: compara com o sinal seco
  fonte arquivo|guitarra         o que alimenta o amp
  canal <n>|media                canal da entrada onde está a guitarra (1, 2, ...)
  trim <dB>                      ganho de entrada da guitarra (−24 a +24)
  latencia                       mede a latência real (precisa de um cabo da saída para a entrada!)
  dispositivos                   lista saídas e entradas
  saida <n> | entrada <n>|nenhuma | buffer <quadros>   reabre a placa
  presets                        lista os presets (fábrica e seus)
  preset <nome>                  carrega um preset
  gravar <nome>                  salva o som atual como preset seu
  salvar <arq.json> | carregar <arq.json|nome>   presets em arquivo
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
	case "ab":
		r := a.s.Renderer
		switch rest {
		case "on":
			r.SetBypass(true)
		case "off":
			r.SetBypass(false)
		default:
			r.SetBypass(!r.Bypass())
		}
		if r.Bypass() {
			fmt.Println("A/B: seco (bypass)")
		} else {
			fmt.Println("A/B: processado")
		}
	case "fonte":
		switch rest {
		case "arquivo":
			a.s.SetSource(audio.SourceFile)
		case "guitarra":
			a.s.SetSource(audio.SourceGuitar)
		default:
			err = fmt.Errorf("use: fonte arquivo|guitarra")
		}
	case "canal":
		if rest == "media" || rest == "média" {
			setCanal(a.s.Live, 0)
		} else {
			var c float64
			if c, err = arg(args, 0); err == nil {
				setCanal(a.s.Live, int(c))
			}
		}
	case "trim":
		var v float64
		if v, err = arg(args, 0); err == nil {
			a.s.Live.Trim().Set(v)
		}
	case "latencia", "latência":
		err = a.latencia()
	case "dispositivos":
		err = listarDispositivos(a.s.Host.Config().Backend)
	case "saida", "entrada", "buffer":
		cfg := a.s.Host.Config()
		var v float64
		switch {
		case cmd == "entrada" && rest == "nenhuma":
			cfg.Input = audio.NoInput
		default:
			if v, err = arg(args, 0); err != nil {
				break
			}
			switch cmd {
			case "saida":
				cfg.Output = int(v)
			case "entrada":
				cfg.Input = int(v)
			case "buffer":
				cfg.Buffer = uint32(v)
			}
		}
		if err == nil {
			if err = a.s.Host.Open(cfg); err == nil {
				if w := a.s.Host.State().Warning; w != "" {
					fmt.Println("aviso:", w)
				}
				a.info()
			}
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
	case "carregar", "preset":
		var st engine.State
		if st, err = presets.Resolve(rest); err == nil {
			err = a.chain.ApplyState(st)
		}
	case "presets":
		dir, _ := presets.Dir()
		list, lerr := presets.List(dir)
		for _, p := range list {
			tipo := "seu"
			if p.Factory {
				tipo = "fábrica"
			}
			fmt.Printf("  %-24s (%s)\n", p.Name, tipo)
		}
		err = lerr
	case "gravar":
		dir, derr := presets.Dir()
		if err = derr; err == nil {
			var p presets.Preset
			if p, err = presets.Save(dir, rest, a.chain.State()); err == nil {
				fmt.Println("salvo em", p.Path)
			}
		}
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
	fmt.Printf("fonte: %s", a.s.Source())
	if a.s.Source() == audio.SourceGuitar {
		c := "média"
		if ch := a.s.Live.Channel(); ch >= 0 {
			c = strconv.Itoa(ch + 1)
		}
		fmt.Printf("  (canal %s, trim %+.1f dB)", c, a.s.Live.Trim().Get())
	}
	fmt.Println()
	fmt.Printf("cadeia: %s\n", a.chain)
	l := a.s.Monitor.Take()
	g := "fechado"
	if l.GateOpen {
		g = "aberto"
	}
	fmt.Printf("placa %6.1f dBFS  entrada %6.1f dBFS  saída %6.1f dBFS  gate %s\n", db(l.InRaw), db(l.InPeak), db(l.OutPeak), g)
	fmt.Printf("clips: placa %d, saída %d  ·  CPU máx %.1f%%  ·  atrasos %d  ·  callbacks %d\n",
		l.InClips, l.Clips, 100*l.MaxLoad, l.Overruns, a.s.Renderer.Callbacks())
}

// info mostra a placa aberta e a latência estimada.
func (a *app) info() {
	st := a.s.Host.State()
	in := "nenhuma"
	if st.Duplex {
		in = fmt.Sprintf("%s (%d canais)", st.InputName, st.InChannels)
	}
	fmt.Printf("ampsim — %s · saída: %s · entrada: %s · %.0f Hz · buffer %d · latência estimada %.1f ms\n",
		st.Backend, st.OutputName, in, st.SampleRate, st.Buffer, a.s.Host.EstimatedLatencyMs(a.chain.Latency()))
}

// latencia roda o teste de loopback e mostra o resultado.
func (a *app) latencia() error {
	fmt.Println("ligue um cabo da saída da placa numa entrada e baixe o volume dos fones/monitores:")
	fmt.Println("vão sair 5 cliques curtos. Medindo...")
	if err := a.s.Renderer.StartLatencyTest(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		res := a.s.Renderer.LatencyResult()
		if !res.Done {
			continue
		}
		if !res.OK {
			return fmt.Errorf("o pulso não voltou (%d de %d medições; pico %.0f dBFS, ruído %.0f dBFS); confira o cabo, o canal da entrada e o volume",
				res.Successes, audio.LatencyAttempts, res.PeakDB, res.NoiseDB)
		}
		cadeia := 1000 * float64(a.chain.Latency()) / a.s.Renderer.SampleRate()
		fmt.Printf("latência de ida e volta: %.1f ms (%.0f amostras, %d de %d medições, variação %.1f ms)\n",
			res.Ms, res.Frames, res.Successes, audio.LatencyAttempts, res.SpreadMs)
		if !res.Consistent {
			fmt.Println("ATENÇÃO: as medições não concordam (algum som entrou junto?); meça de novo em silêncio")
		}
		fmt.Printf("com a cadeia de efeitos (+%.1f ms): %.1f ms da corda ao ouvido\n", cadeia, res.Ms+cadeia)
		return nil
	}
	return fmt.Errorf("o teste não terminou")
}

func (a *app) medir(secs float64) {
	a.s.Monitor.Take()
	end := time.Now().Add(time.Duration(secs * float64(time.Second)))
	for time.Now().Before(end) {
		time.Sleep(200 * time.Millisecond)
		l := a.s.Monitor.Take()
		g := "·"
		if l.GateOpen {
			g = "G"
		}
		fmt.Printf("placa %6.1f  in %s %6.1f  out %s %6.1f  %s  cpu %4.1f%%  clips %d  atrasos %d\n",
			db(l.InRaw), bar(l.InPeak), db(l.InPeak), bar(l.OutPeak), db(l.OutPeak), g, 100*l.MaxLoad, l.Clips, l.Overruns)
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
