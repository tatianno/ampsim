// Camada C do plugin CLAP: o ponto de entrada, a fábrica e as extensões (portas de áudio,
// parâmetros, estado, latência). Todo o resto fica no Go (clap.go).

#include <stdlib.h>
#include <string.h>

#include <clap/clap.h>

#include "_cgo_export.h"

#define AMPSIM_CLAP_ID "com.github.tatianno.ampsim"

typedef struct {
	clap_plugin_t plugin;
	const clap_host_t *host;
	uintptr_t go;
} Plugin;

static uintptr_t go_of(const clap_plugin_t *p) { return ((Plugin *)p->plugin_data)->go; }

// ---------------------------------------------------------------- portas de áudio

static uint32_t ports_count(const clap_plugin_t *p, bool is_input) {
	(void)p;
	(void)is_input;
	return 1;
}

static bool ports_get(const clap_plugin_t *p, uint32_t index, bool is_input,
                      clap_audio_port_info_t *info) {
	(void)p;
	if (index != 0) {
		return false;
	}
	info->id = is_input ? 0 : 1;
	strncpy(info->name, is_input ? "Entrada" : "Saída", sizeof(info->name) - 1);
	info->name[sizeof(info->name) - 1] = 0;
	info->flags = CLAP_AUDIO_PORT_IS_MAIN;
	info->channel_count = 2;
	info->port_type = CLAP_PORT_STEREO;
	info->in_place_pair = CLAP_INVALID_ID;
	return true;
}

static const clap_plugin_audio_ports_t audio_ports = {ports_count, ports_get};

// ---------------------------------------------------------------- parâmetros

static void param_events(uintptr_t go, const clap_input_events_t *in) {
	if (!in) {
		return;
	}
	uint32_t n = in->size(in);
	for (uint32_t i = 0; i < n; i++) {
		const clap_event_header_t *h = in->get(in, i);
		if (h->space_id == CLAP_CORE_EVENT_SPACE_ID && h->type == CLAP_EVENT_PARAM_VALUE) {
			const clap_event_param_value_t *e = (const clap_event_param_value_t *)h;
			ampsimClapParam(go, e->param_id, e->value);
		}
	}
}

static uint32_t params_count(const clap_plugin_t *p) {
	(void)p;
	return 4;
}

static bool params_info(const clap_plugin_t *p, uint32_t index, clap_param_info_t *info) {
	return ampsimClapParamInfo(go_of(p), index, info);
}

static bool params_value(const clap_plugin_t *p, clap_id id, double *out) {
	return ampsimClapValue(go_of(p), id, out);
}

static bool params_to_text(const clap_plugin_t *p, clap_id id, double v, char *buf, uint32_t n) {
	return ampsimClapText(go_of(p), id, v, buf, n);
}

static bool params_from_text(const clap_plugin_t *p, clap_id id, const char *text, double *out) {
	return ampsimClapParse(go_of(p), id, (char *)text, out);
}

static void params_flush(const clap_plugin_t *p, const clap_input_events_t *in,
                         const clap_output_events_t *out) {
	(void)out;
	param_events(go_of(p), in);
}

static const clap_plugin_params_t params = {params_count,   params_info,      params_value,
                                            params_to_text, params_from_text, params_flush};

// ---------------------------------------------------------------- estado

static bool state_save(const clap_plugin_t *p, const clap_ostream_t *s) {
	size_t size = 0;
	char *data = ampsimClapSave(go_of(p), &size);
	if (!data) {
		return false;
	}
	size_t off = 0;
	while (off < size) {
		int64_t w = s->write(s, data + off, size - off);
		if (w <= 0) {
			free(data);
			return false;
		}
		off += (size_t)w;
	}
	free(data);
	return true;
}

static bool state_load(const clap_plugin_t *p, const clap_istream_t *s) {
	size_t cap = 4096, size = 0;
	char *data = malloc(cap);
	for (;;) {
		if (!data) {
			return false;
		}
		if (size == cap) {
			cap *= 2;
			char *d = realloc(data, cap);
			if (!d) {
				free(data);
				return false;
			}
			data = d;
		}
		int64_t r = s->read(s, data + size, cap - size);
		if (r < 0) {
			free(data);
			return false;
		}
		if (r == 0) {
			break;
		}
		size += (size_t)r;
	}
	bool ok = ampsimClapLoad(go_of(p), data, size);
	free(data);
	if (ok) { // o texto do Preset pode ter mudado (a cópia de um preset que não existe mais)
		Plugin *pl = p->plugin_data;
		const clap_host_params_t *hp = pl->host->get_extension(pl->host, CLAP_EXT_PARAMS);
		if (hp && hp->rescan) {
			hp->rescan(pl->host, CLAP_PARAM_RESCAN_VALUES | CLAP_PARAM_RESCAN_TEXT);
		}
	}
	return ok;
}

static const clap_plugin_state_t state = {state_save, state_load};

// ---------------------------------------------------------------- latência

static uint32_t latency_get(const clap_plugin_t *p) { return ampsimClapLatency(go_of(p)); }

static const clap_plugin_latency_t latency = {latency_get};

// ---------------------------------------------------------------- plugin

static bool plugin_init(const clap_plugin_t *p) {
	Plugin *pl = p->plugin_data;
	pl->go = ampsimClapNew();
	return pl->go != 0;
}

static void plugin_destroy(const clap_plugin_t *p) {
	Plugin *pl = p->plugin_data;
	if (pl->go) {
		ampsimClapFree(pl->go);
	}
	free(pl);
}

static bool plugin_activate(const clap_plugin_t *p, double rate, uint32_t min_frames,
                            uint32_t max_frames) {
	(void)min_frames;
	(void)max_frames;
	return ampsimClapActivate(go_of(p), rate);
}

static void plugin_deactivate(const clap_plugin_t *p) { ampsimClapDeactivate(go_of(p)); }

static bool plugin_start(const clap_plugin_t *p) {
	(void)p;
	return true;
}

static void plugin_stop(const clap_plugin_t *p) { (void)p; }

static void plugin_reset(const clap_plugin_t *p) { ampsimClapReset(go_of(p)); }

static clap_process_status plugin_process(const clap_plugin_t *p, const clap_process_t *proc) {
	uintptr_t go = go_of(p);
	param_events(go, proc->in_events);
	if (proc->audio_outputs_count < 1) {
		return CLAP_PROCESS_CONTINUE;
	}
	const clap_audio_buffer_t *out = &proc->audio_outputs[0];
	float **in = NULL;
	uint32_t nin = 0;
	if (proc->audio_inputs_count > 0) {
		in = proc->audio_inputs[0].data32;
		nin = proc->audio_inputs[0].channel_count;
	}
	ampsimClapProcess(go, in, nin, out->data32, out->channel_count, proc->frames_count);
	return CLAP_PROCESS_CONTINUE;
}

static const void *plugin_extension(const clap_plugin_t *p, const char *id) {
	(void)p;
	if (strcmp(id, CLAP_EXT_AUDIO_PORTS) == 0) {
		return &audio_ports;
	}
	if (strcmp(id, CLAP_EXT_PARAMS) == 0) {
		return &params;
	}
	if (strcmp(id, CLAP_EXT_STATE) == 0) {
		return &state;
	}
	if (strcmp(id, CLAP_EXT_LATENCY) == 0) {
		return &latency;
	}
	return NULL;
}

static void plugin_main_thread(const clap_plugin_t *p) { (void)p; }

// ---------------------------------------------------------------- fábrica e entrada

static const char *features[] = {CLAP_PLUGIN_FEATURE_AUDIO_EFFECT, CLAP_PLUGIN_FEATURE_DISTORTION,
                                 CLAP_PLUGIN_FEATURE_STEREO, NULL};

static clap_plugin_descriptor_t descriptor = {
	.clap_version = CLAP_VERSION_INIT,
	.id = AMPSIM_CLAP_ID,
	.name = "AmpSim",
	.vendor = "AmpSim",
	.url = "https://github.com/tatianno/ampsim",
	.manual_url = "https://github.com/tatianno/ampsim#plugin-lv2",
	.support_url = "https://github.com/tatianno/ampsim/issues",
	.version = "dev",
	.description = "Aplica um preset do AmpSim (amp, gabinete e pedais) à faixa, com a troca de "
	               "preset ao vivo. Os presets são editados no app AmpSim.",
	.features = features,
};

static uint32_t factory_count(const clap_plugin_factory_t *f) {
	(void)f;
	return 1;
}

static const clap_plugin_descriptor_t *factory_descriptor(const clap_plugin_factory_t *f,
                                                          uint32_t index) {
	(void)f;
	if (index != 0) {
		return NULL;
	}
	descriptor.version = ampsimVersion();
	return &descriptor;
}

static const clap_plugin_t *factory_create(const clap_plugin_factory_t *f, const clap_host_t *host,
                                           const char *id) {
	(void)f;
	if (!clap_version_is_compatible(host->clap_version) || strcmp(id, AMPSIM_CLAP_ID) != 0) {
		return NULL;
	}
	Plugin *pl = calloc(1, sizeof(Plugin));
	if (!pl) {
		return NULL;
	}
	pl->host = host;
	pl->plugin = (clap_plugin_t){
		.desc = factory_descriptor(f, 0),
		.plugin_data = pl,
		.init = plugin_init,
		.destroy = plugin_destroy,
		.activate = plugin_activate,
		.deactivate = plugin_deactivate,
		.start_processing = plugin_start,
		.stop_processing = plugin_stop,
		.reset = plugin_reset,
		.process = plugin_process,
		.get_extension = plugin_extension,
		.on_main_thread = plugin_main_thread,
	};
	return &pl->plugin;
}

static const clap_plugin_factory_t factory = {factory_count, factory_descriptor, factory_create};

static bool entry_init(const char *path) {
	(void)path;
	return true;
}

static void entry_deinit(void) {}

static const void *entry_factory(const char *id) {
	return strcmp(id, CLAP_PLUGIN_FACTORY_ID) == 0 ? &factory : NULL;
}

CLAP_EXPORT const clap_plugin_entry_t clap_entry = {
	.clap_version = CLAP_VERSION_INIT,
	.init = entry_init,
	.deinit = entry_deinit,
	.get_factory = entry_factory,
};
