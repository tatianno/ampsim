// hostcheck: host LV2 mínimo para testar o bundle sem DAW, pela lilv do sistema (a mesma
// biblioteca que o Audacity usa para achar e carregar plugins LV2). Lista as portas, processa
// um arquivo float32 cru em blocos de tamanho variável (descontando a latência informada,
// como o Audacity) e confere o estado: salva, restaura numa instância nova e compara.
//
// Uso (veja check.sh): LV2_PATH=dist/lv2 hostcheck URI taxa entrada.f32 saida.f32
//
// As declarações da lilv ficam aqui (o pacote liblilv-dev não está instalado); o build liga
// direto com /usr/lib/x86_64-linux-gnu/liblilv-0.so.0.

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "../lv2min.h"

typedef struct LilvWorldImpl LilvWorld;
typedef struct LilvPluginImpl LilvPlugin;
typedef struct LilvPluginsImpl LilvPlugins;
typedef struct LilvPortImpl LilvPort;
typedef struct LilvNodeImpl LilvNode;
typedef struct LilvScalePointsImpl LilvScalePoints;
typedef struct {
	const LV2_Descriptor *lv2_descriptor;
	LV2_Handle lv2_handle;
	void *pimpl;
} LilvInstance;

LilvWorld *lilv_world_new(void);
void lilv_world_load_all(LilvWorld *);
const LilvPlugins *lilv_world_get_all_plugins(const LilvWorld *);
LilvNode *lilv_new_uri(LilvWorld *, const char *);
const LilvPlugin *lilv_plugins_get_by_uri(const LilvPlugins *, const LilvNode *);
LilvNode *lilv_plugin_get_name(const LilvPlugin *);
unsigned lilv_plugin_get_num_ports(const LilvPlugin *);
const LilvPort *lilv_plugin_get_port_by_index(const LilvPlugin *, unsigned);
const LilvNode *lilv_port_get_symbol(const LilvPlugin *, const LilvPort *);
LilvNode *lilv_port_get_name(const LilvPlugin *, const LilvPort *);
void lilv_port_get_range(const LilvPlugin *, const LilvPort *, LilvNode **, LilvNode **, LilvNode **);
LilvScalePoints *lilv_port_get_scale_points(const LilvPlugin *, const LilvPort *);
unsigned lilv_scale_points_size(const LilvScalePoints *);
void lilv_scale_points_free(LilvScalePoints *);
int lilv_port_is_a(const LilvPlugin *, const LilvPort *, const LilvNode *);
int lilv_port_has_property(const LilvPlugin *, const LilvPort *, const LilvNode *);
const char *lilv_node_as_string(const LilvNode *);
float lilv_node_as_float(const LilvNode *);
void lilv_node_free(LilvNode *);
LilvInstance *lilv_plugin_instantiate(const LilvPlugin *, double, const LV2_Feature *const *);
void lilv_instance_free(LilvInstance *);

// urid:map simples
static char *uris[64];
static int nuris;
static LV2_URID map_uri(LV2_URID_Map_Handle h, const char *uri) {
	(void)h;
	for (int i = 0; i < nuris; i++) {
		if (strcmp(uris[i], uri) == 0) {
			return i + 1;
		}
	}
	uris[nuris] = strdup(uri);
	return ++nuris;
}

// estado guardado em memória (uma chave basta)
static struct {
	uint32_t key, type, flags;
	void *value;
	size_t size;
} saved;

static LV2_State_Status store(LV2_State_Handle h, uint32_t key, const void *value, size_t size,
                              uint32_t type, uint32_t flags) {
	(void)h;
	saved.key = key;
	saved.type = type;
	saved.flags = flags;
	saved.size = size;
	saved.value = malloc(size);
	memcpy(saved.value, value, size);
	return LV2_STATE_SUCCESS;
}

static const void *retrieve(LV2_State_Handle h, uint32_t key, size_t *size, uint32_t *type,
                            uint32_t *flags) {
	(void)h;
	if (!saved.value || key != saved.key) {
		return NULL;
	}
	*size = saved.size;
	*type = saved.type;
	*flags = saved.flags;
	return saved.value;
}

#define MAXP 16

typedef struct {
	LilvInstance *inst;
	float ctl[MAXP];
	int nin, nout, inIdx[2], outIdx[2], latIdx;
} Host;

static const LilvPlugin *plug;
static LilvNode *nAudio, *nInput, *nOutput, *nLatency;

static double rate;

// open_host cria a instância; o Host fica no heap (as portas de controle apontam para ele).
static Host *open_host(const LV2_Feature *const *features) {
	Host *hp = calloc(1, sizeof(Host));
	hp->inst = lilv_plugin_instantiate(plug, rate, features);
	if (!hp->inst) {
		fprintf(stderr, "instantiate falhou\n");
		exit(1);
	}
	hp->latIdx = -1;
	unsigned np = lilv_plugin_get_num_ports(plug);
	for (unsigned i = 0; i < np; i++) {
		const LilvPort *p = lilv_plugin_get_port_by_index(plug, i);
		if (lilv_port_is_a(plug, p, nAudio)) {
			if (lilv_port_is_a(plug, p, nInput)) {
				hp->inIdx[hp->nin++] = i;
			} else {
				hp->outIdx[hp->nout++] = i;
			}
			continue;
		}
		LilvNode *def = NULL;
		lilv_port_get_range(plug, p, &def, NULL, NULL);
		hp->ctl[i] = def ? lilv_node_as_float(def) : 0;
		lilv_node_free(def);
		if (lilv_port_has_property(plug, p, nLatency)) {
			hp->latIdx = i;
		}
		hp->inst->lv2_descriptor->connect_port(hp->inst->lv2_handle, i, &hp->ctl[i]);
	}
	if (hp->inst->lv2_descriptor->activate) {
		hp->inst->lv2_descriptor->activate(hp->inst->lv2_handle);
	}
	return hp;
}

// process roda x inteiro em blocos de 1024, 333 e 4096 amostras (alternando) e devolve a
// saída (canal 0) sem a latência.
static float *process(Host *h, const float *x, long n, int *lat) {
	long total = n + 16384;
	float *in = calloc(total, sizeof(float)), *out[2];
	memcpy(in, x, n * sizeof(float));
	for (int c = 0; c < h->nout; c++) {
		out[c] = calloc(total, sizeof(float));
	}
	const int sizes[] = {1024, 333, 4096};
	for (long off = 0, k = 0; off < total; k++) {
		long b = sizes[k % 3];
		if (off + b > total) {
			b = total - off;
		}
		for (int c = 0; c < h->nin; c++) {
			h->inst->lv2_descriptor->connect_port(h->inst->lv2_handle, h->inIdx[c], in + off);
		}
		for (int c = 0; c < h->nout; c++) {
			h->inst->lv2_descriptor->connect_port(h->inst->lv2_handle, h->outIdx[c], out[c] + off);
		}
		h->inst->lv2_descriptor->run(h->inst->lv2_handle, b);
		off += b;
	}
	*lat = h->latIdx >= 0 ? (int)h->ctl[h->latIdx] : 0;
	float *y = calloc(n, sizeof(float));
	memcpy(y, out[0] + *lat, n * sizeof(float));
	return y;
}

int main(int argc, char **argv) {
	if (argc != 5) {
		fprintf(stderr, "uso: hostcheck URI taxa entrada.f32 saida.f32\n");
		return 2;
	}
	rate = atof(argv[2]);
	LilvWorld *w = lilv_world_new();
	lilv_world_load_all(w);
	LilvNode *uri = lilv_new_uri(w, argv[1]);
	plug = lilv_plugins_get_by_uri(lilv_world_get_all_plugins(w), uri);
	if (!plug) {
		fprintf(stderr, "plugin %s não encontrado (LV2_PATH?)\n", argv[1]);
		return 1;
	}
	nAudio = lilv_new_uri(w, "http://lv2plug.in/ns/lv2core#AudioPort");
	nInput = lilv_new_uri(w, "http://lv2plug.in/ns/lv2core#InputPort");
	nOutput = lilv_new_uri(w, "http://lv2plug.in/ns/lv2core#OutputPort");
	nLatency = lilv_new_uri(w, "http://lv2plug.in/ns/lv2core#reportsLatency");

	LilvNode *name = lilv_plugin_get_name(plug);
	printf("plugin: %s\n", lilv_node_as_string(name));
	unsigned np = lilv_plugin_get_num_ports(plug);
	for (unsigned i = 0; i < np; i++) {
		const LilvPort *p = lilv_plugin_get_port_by_index(plug, i);
		LilvNode *pn = lilv_port_get_name(plug, p), *def = NULL, *mn = NULL, *mx = NULL;
		lilv_port_get_range(plug, p, &def, &mn, &mx);
		LilvScalePoints *sp = lilv_port_get_scale_points(plug, p);
		printf("  porta %u %-9s %-10s", i, lilv_node_as_string(lilv_port_get_symbol(plug, p)),
		       lilv_node_as_string(pn));
		if (mn && mx) {
			printf(" [%g, %g] padrão %g", lilv_node_as_float(mn), lilv_node_as_float(mx),
			       def ? lilv_node_as_float(def) : 0);
		}
		if (sp) {
			printf(" (%u opções)", lilv_scale_points_size(sp));
			lilv_scale_points_free(sp);
		}
		printf("\n");
	}

	FILE *f = fopen(argv[3], "rb");
	if (!f) {
		perror(argv[3]);
		return 1;
	}
	fseek(f, 0, SEEK_END);
	long n = ftell(f) / sizeof(float);
	fseek(f, 0, SEEK_SET);
	float *x = malloc(n * sizeof(float));
	if (fread(x, sizeof(float), n, f) != (size_t)n) {
		return 1;
	}
	fclose(f);

	LV2_URID_Map map = {NULL, map_uri};
	LV2_Feature fmap = {LV2_URID__map, &map};
	const LV2_Feature *features[] = {&fmap, NULL};

	Host *a = open_host(features);
	int lat;
	float *y = process(a, x, n, &lat);
	printf("latência informada: %d amostras\n", lat);
	f = fopen(argv[4], "wb");
	fwrite(y, sizeof(float), n, f);
	fclose(f);

	// estado: salvar, restaurar numa instância nova e comparar
	const LV2_State_Interface *st = a->inst->lv2_descriptor->extension_data(LV2_STATE__interface);
	if (!st || st->save(a->inst->lv2_handle, store, NULL, LV2_STATE_IS_POD | LV2_STATE_IS_PORTABLE,
	                    features) != LV2_STATE_SUCCESS || !saved.value) {
		fprintf(stderr, "state: save falhou\n");
		return 1;
	}
	printf("estado: %zu bytes, começa com %.60s…\n", saved.size, (char *)saved.value);
	Host *b = open_host(features);
	if (st->restore(b->inst->lv2_handle, retrieve, NULL, 0, features) != LV2_STATE_SUCCESS) {
		fprintf(stderr, "state: restore falhou\n");
		return 1;
	}
	int lat2;
	float *y2 = process(b, x, n, &lat2);
	double maxd = 0;
	for (long i = 0; i < n; i++) {
		maxd = fmax(maxd, fabs(y[i] - y2[i]));
	}
	printf("restaurado: latência %d, diferença máxima para a original %.3g\n", lat2, maxd);
	a->inst->lv2_descriptor->cleanup(a->inst->lv2_handle);
	b->inst->lv2_descriptor->cleanup(b->inst->lv2_handle);
	return maxd > 1e-6 || lat2 != lat;
}
