// clapcheck: host CLAP mínimo para testar o plugin sem DAW. Carrega o .so, lista os
// parâmetros, escolhe o preset pelo nome (antes do activate, pelo flush), processa um arquivo
// float32 cru em estéreo, em blocos de tamanho variável (descontando a latência informada),
// troca de preset com o áudio rodando e confere o estado: salva, restaura numa instância nova
// e compara.
//
// Uso (veja check.sh): clapcheck ampsim.so "Nome do preset" taxa entrada.f32 saida.f32
//   saida.f32: o canal L do primeiro processamento (sem a latência)

#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <clap/clap.h>

// ---------------------------------------------------------------- host

static const void *host_ext(const clap_host_t *h, const char *id) {
	(void)h;
	(void)id;
	return NULL;
}
static void host_noop(const clap_host_t *h) { (void)h; }

static const clap_host_t host = {
	.clap_version = CLAP_VERSION_INIT,
	.name = "clapcheck",
	.vendor = "AmpSim",
	.url = "",
	.version = "1",
	.get_extension = host_ext,
	.request_restart = host_noop,
	.request_process = host_noop,
	.request_callback = host_noop,
};

// lista de eventos de entrada (um evento de parâmetro no máximo)
static clap_event_param_value_t pending;
static int has_pending;

static uint32_t ev_size(const clap_input_events_t *l) {
	(void)l;
	return has_pending;
}
static const clap_event_header_t *ev_get(const clap_input_events_t *l, uint32_t i) {
	(void)l;
	(void)i;
	return &pending.header;
}
static const clap_input_events_t in_events = {NULL, ev_size, ev_get};
static bool out_push(const clap_output_events_t *l, const clap_event_header_t *e) {
	(void)l;
	(void)e;
	return true;
}
static const clap_output_events_t out_events = {NULL, out_push};

static void set_param(clap_id id, double v) {
	memset(&pending, 0, sizeof(pending));
	pending.header.size = sizeof(pending);
	pending.header.type = CLAP_EVENT_PARAM_VALUE;
	pending.header.space_id = CLAP_CORE_EVENT_SPACE_ID;
	pending.param_id = id;
	pending.note_id = -1;
	pending.port_index = pending.channel = pending.key = -1;
	pending.value = v;
	has_pending = 1;
}

// streams em memória
static char *saved;
static size_t saved_size, read_pos;
static int64_t ost_write(const clap_ostream_t *s, const void *b, uint64_t n) {
	(void)s;
	saved = realloc(saved, saved_size + n);
	memcpy(saved + saved_size, b, n);
	saved_size += n;
	return (int64_t)n;
}
static int64_t ist_read(const clap_istream_t *s, void *b, uint64_t n) {
	(void)s;
	uint64_t k = saved_size - read_pos < n ? saved_size - read_pos : n;
	memcpy(b, saved + read_pos, k);
	read_pos += k;
	return (int64_t)k;
}

// ---------------------------------------------------------------- plugin

static const clap_plugin_factory_t *factory;
static double rate;

typedef struct {
	const clap_plugin_t *p;
	const clap_plugin_params_t *params;
	const clap_plugin_state_t *state;
	const clap_plugin_latency_t *latency;
} Inst;

static Inst open_plugin(void) {
	Inst in = {0};
	const clap_plugin_descriptor_t *d = factory->get_plugin_descriptor(factory, 0);
	in.p = factory->create_plugin(factory, &host, d->id);
	if (!in.p || !in.p->init(in.p)) {
		fprintf(stderr, "create/init falhou\n");
		exit(1);
	}
	in.params = in.p->get_extension(in.p, CLAP_EXT_PARAMS);
	in.state = in.p->get_extension(in.p, CLAP_EXT_STATE);
	in.latency = in.p->get_extension(in.p, CLAP_EXT_LATENCY);
	if (!in.params || !in.state || !in.latency ||
	    !in.p->get_extension(in.p, CLAP_EXT_AUDIO_PORTS)) {
		fprintf(stderr, "faltam extensões\n");
		exit(1);
	}
	return in;
}

static void activate(Inst *in) {
	if (!in->p->activate(in->p, rate, 1, 4096) || !in->p->start_processing(in->p)) {
		fprintf(stderr, "activate falhou\n");
		exit(1);
	}
}

// process roda x (n amostras) em estéreo, em blocos de 1024, 333 e 4096; switch_at >= 0 manda
// o evento pendente nesse bloco. Devolve o canal L e o R inteiros (com a latência).
static void process(Inst *in, const float *x, long n, float *outL, float *outR, int switch_at,
                    clap_id sw_id, double sw_value) {
	const int sizes[] = {1024, 333, 4096};
	float *inL = calloc(4096, sizeof(float)), *inR = calloc(4096, sizeof(float));
	for (long off = 0, k = 0; off < n; k++) {
		long b = sizes[k % 3];
		if (off + b > n) {
			b = n - off;
		}
		memcpy(inL, x + off, b * sizeof(float));
		memcpy(inR, x + off, b * sizeof(float));
		float *ins[2] = {inL, inR}, *outs[2] = {outL + off, outR + off};
		clap_audio_buffer_t ain = {.data32 = ins, .channel_count = 2};
		clap_audio_buffer_t aout = {.data32 = outs, .channel_count = 2};
		if (k == switch_at) {
			set_param(sw_id, sw_value);
		}
		clap_process_t proc = {
			.steady_time = off,
			.frames_count = (uint32_t)b,
			.audio_inputs = &ain,
			.audio_outputs = &aout,
			.audio_inputs_count = 1,
			.audio_outputs_count = 1,
			.in_events = &in_events,
			.out_events = &out_events,
		};
		if (in->p->process(in->p, &proc) == CLAP_PROCESS_ERROR) {
			fprintf(stderr, "process falhou\n");
			exit(1);
		}
		has_pending = 0;
		off += b;
	}
	free(inL);
	free(inR);
}

int main(int argc, char **argv) {
	if (argc != 6) {
		fprintf(stderr, "uso: clapcheck ampsim.so \"preset\" taxa entrada.f32 saida.f32\n");
		return 2;
	}
	rate = atof(argv[3]);
	void *lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
	if (!lib) {
		fprintf(stderr, "dlopen: %s\n", dlerror());
		return 1;
	}
	const clap_plugin_entry_t *entry = dlsym(lib, "clap_entry");
	if (!entry || !entry->init(argv[1])) {
		fprintf(stderr, "sem clap_entry\n");
		return 1;
	}
	factory = entry->get_factory(CLAP_PLUGIN_FACTORY_ID);
	const clap_plugin_descriptor_t *d = factory->get_plugin_descriptor(factory, 0);
	printf("plugin: %s (%s) versão %s, %u plugin(s)\n", d->name, d->id, d->version,
	       factory->get_plugin_count(factory));

	Inst a = open_plugin();
	clap_param_info_t info;
	for (uint32_t i = 0; i < a.params->count(a.p); i++) {
		a.params->get_info(a.p, i, &info);
		double v;
		char txt[256];
		a.params->get_value(a.p, info.id, &v);
		a.params->value_to_text(a.p, info.id, v, txt, sizeof(txt));
		printf("  parâmetro %u %-8s [%g, %g] padrão %g = %s%s%s\n", info.id, info.name,
		       info.min_value, info.max_value, info.default_value, txt,
		       info.flags & CLAP_PARAM_IS_ENUM ? " (lista)" : "",
		       info.flags & CLAP_PARAM_IS_BYPASS ? " (bypass)" : "");
	}
	double idx;
	if (!a.params->text_to_value(a.p, 0, argv[2], &idx)) {
		fprintf(stderr, "preset %s não está na lista\n", argv[2]);
		return 1;
	}
	set_param(0, idx); // antes do activate: pelo flush, na thread principal
	a.params->flush(a.p, &in_events, &out_events);
	has_pending = 0;
	activate(&a);
	uint32_t lat = a.latency->get(a.p);
	printf("preset %s = %g; latência informada: %u amostras\n", argv[2], idx, lat);

	FILE *f = fopen(argv[4], "rb");
	if (!f) {
		perror(argv[4]);
		return 1;
	}
	fseek(f, 0, SEEK_END);
	long n = ftell(f) / sizeof(float);
	fseek(f, 0, SEEK_SET);
	long total = n + lat;
	float *x = calloc(total, sizeof(float));
	if (fread(x, sizeof(float), n, f) != (size_t)n) {
		return 1;
	}
	fclose(f);

	float *L = calloc(total, sizeof(float)), *R = calloc(total, sizeof(float));
	process(&a, x, total, L, R, -1, 0, 0);
	f = fopen(argv[5], "wb");
	fwrite(L + lat, sizeof(float), n, f);
	fclose(f);
	double lr = 0;
	for (long i = 0; i < total; i++) {
		lr = fmax(lr, fabs(L[i] - R[i]));
	}
	printf("estéreo: maior diferença L-R %.3g\n", lr);

	// estado: salvar, restaurar numa instância nova (antes do activate) e comparar
	if (!a.state->save(a.p, &(clap_ostream_t){NULL, ost_write})) {
		fprintf(stderr, "state: save falhou\n");
		return 1;
	}
	printf("estado: %zu bytes, começa com %.70s…\n", saved_size, saved);
	Inst b = open_plugin();
	read_pos = 0;
	if (!b.state->load(b.p, &(clap_istream_t){NULL, ist_read})) {
		fprintf(stderr, "state: load falhou\n");
		return 1;
	}
	double bv;
	char btxt[256];
	b.params->get_value(b.p, 0, &bv);
	b.params->value_to_text(b.p, 0, bv, btxt, sizeof(btxt));
	activate(&b);
	float *L2 = calloc(total, sizeof(float)), *R2 = calloc(total, sizeof(float));
	process(&b, x, total, L2, R2, -1, 0, 0);
	double maxd = 0;
	for (long i = 0; i < total; i++) {
		maxd = fmax(maxd, fmax(fabs(L[i] - L2[i]), fabs(R[i] - R2[i])));
	}
	printf("restaurado: Preset = %s, diferença máxima para a original %.3g\n", btxt, maxd);

	// troca ao vivo: no meio do arquivo, o preset vai para o último de fábrica da lista lida
	// (o valor 0 = o primeiro); o som muda e a latência não
	double other = idx == 0 ? 11 : 0;
	process(&b, x, total, L2, R2, 20, 0, other);
	long mid = 20 * 1800; // depois do evento (os blocos têm em média ~1800 amostras)
	b.params->get_value(b.p, 0, &bv);
	b.params->value_to_text(b.p, 0, bv, btxt, sizeof(btxt));
	double d2 = 0;
	for (long i = mid + 48000 / 2; i < total; i++) {
		d2 = fmax(d2, fabs(L2[i] - L[i]));
	}
	uint32_t lat2 = b.latency->get(b.p);
	printf("troca ao vivo para %s: diferença depois da troca %.3g, latência %u\n", btxt, d2, lat2);

	b.p->stop_processing(b.p);
	b.p->deactivate(b.p);
	b.p->destroy(b.p);
	a.p->stop_processing(a.p);
	a.p->deactivate(a.p);
	a.p->destroy(a.p);
	entry->deinit();
	return maxd > 1e-6 || d2 < 1e-3 || lat2 != lat;
}
