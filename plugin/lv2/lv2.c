// Camada C do plugin LV2: os descritores e o estado. Todo o resto (portas, áudio, presets)
// fica no Go (main.go); aqui só se repassam as chamadas do host.

#include <stdlib.h>
#include <string.h>

#include "_cgo_export.h"
#include "lv2min.h"

#define MAX_PORTS 64 // o pedal com mais knobs (synth) tem 20 + 4

typedef struct {
	uintptr_t go;
	float *ports[MAX_PORTS];
	LV2_URID key;      // chave do estado (0 = sem urid:map: sem estado)
	LV2_URID t_string; // atom:String
} Plugin;

static LV2_Handle instantiate(const LV2_Descriptor *d, double rate, const char *bundle,
                              const LV2_Feature *const *features) {
	(void)bundle;
	Plugin *p = calloc(1, sizeof(Plugin));
	if (!p) {
		return NULL;
	}
	for (int i = 0; features && features[i]; i++) {
		if (strcmp(features[i]->URI, LV2_URID__map) == 0) {
			LV2_URID_Map *m = features[i]->data;
			p->key = m->map(m->handle, ampsimStateKey());
			p->t_string = m->map(m->handle, LV2_ATOM__String);
		}
	}
	p->go = ampsimNew(rate, (char *)d->URI);
	if (!p->go) {
		free(p);
		return NULL;
	}
	return p;
}

static void connect_port(LV2_Handle h, uint32_t port, void *data) {
	Plugin *p = h;
	if (port < MAX_PORTS) {
		p->ports[port] = data;
	}
}

static void activate(LV2_Handle h) {
	Plugin *p = h;
	ampsimActivate(p->go, p->ports);
}

static void run(LV2_Handle h, uint32_t n) {
	Plugin *p = h;
	ampsimRun(p->go, p->ports, n);
}

static void cleanup(LV2_Handle h) {
	Plugin *p = h;
	ampsimFree(p->go);
	free(p);
}

static LV2_State_Status save(LV2_Handle h, LV2_State_Store_Function store, LV2_State_Handle sh,
                             uint32_t flags, const LV2_Feature *const *features) {
	(void)flags;
	(void)features;
	Plugin *p = h;
	if (!p->key) {
		return LV2_STATE_ERR_NO_FEATURE;
	}
	size_t size = 0;
	char *json = ampsimSave(p->go, &size); // com o '\0' (atom:String)
	if (!json) {
		return LV2_STATE_SUCCESS; // nada aplicado ainda: nada a salvar
	}
	LV2_State_Status st = store(sh, p->key, json, size, p->t_string,
	                            LV2_STATE_IS_POD | LV2_STATE_IS_PORTABLE);
	free(json);
	return st;
}

static LV2_State_Status restore(LV2_Handle h, LV2_State_Retrieve_Function retrieve,
                                LV2_State_Handle sh, uint32_t flags,
                                const LV2_Feature *const *features) {
	(void)flags;
	(void)features;
	Plugin *p = h;
	if (!p->key) {
		return LV2_STATE_ERR_NO_FEATURE;
	}
	size_t size = 0;
	uint32_t type = 0, vflags = 0;
	const char *json = retrieve(sh, p->key, &size, &type, &vflags);
	if (!json) {
		return LV2_STATE_ERR_NO_PROPERTY;
	}
	if (type != p->t_string) {
		return LV2_STATE_ERR_BAD_TYPE;
	}
	return ampsimRestore(p->go, (char *)json, size) ? LV2_STATE_SUCCESS : LV2_STATE_ERR_UNKNOWN;
}

static const LV2_State_Interface state_iface = {save, restore};

static const void *extension_data(const char *uri) {
	if (strcmp(uri, LV2_STATE__interface) == 0) {
		return &state_iface;
	}
	return NULL;
}

// Um descritor por preset (a lista é lida uma vez, ao carregar o .so).
static LV2_Descriptor *descriptors;
static uint32_t num_descriptors;

__attribute__((visibility("default"))) const LV2_Descriptor *lv2_descriptor(uint32_t index) {
	if (!descriptors) {
		num_descriptors = (uint32_t)ampsimNumPlugins();
		descriptors = calloc(num_descriptors ? num_descriptors : 1, sizeof(LV2_Descriptor));
		if (!descriptors) {
			return NULL;
		}
	}
	if (index >= num_descriptors) {
		return NULL;
	}
	LV2_Descriptor *d = &descriptors[index];
	if (!d->URI) {
		d->URI = ampsimPluginURI(index); // alocada uma vez, fica até o fim do processo
		d->instantiate = instantiate;
		d->connect_port = connect_port;
		d->activate = activate;
		d->run = run;
		d->cleanup = cleanup;
		d->extension_data = extension_data;
	}
	return d;
}
