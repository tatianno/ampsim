// Camada C do backend JACK próprio (Linux).
//
// A libjack é carregada com dlopen (como o miniaudio faz): não precisa de headers para
// compilar e o app funciona sem JACK instalado. Com o PipeWire, carregamos direto a
// libjack dele (pipewire-jack), sem precisar do pw-jack.
//
// Um único cliente por processo. O callback de processamento pega os buffers das portas
// e chama ampsimJackProcess (Go), que roda o engine.

#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "_cgo_export.h"

typedef struct _jack_client jack_client_t;
typedef struct _jack_port jack_port_t;
typedef uint32_t jack_nframes_t;
typedef int (*JackProcessCallback)(jack_nframes_t nframes, void *arg);
typedef void (*JackShutdownCallback)(void *arg);

#define JackNoStartServer 0x01
#define JackPortIsInput 0x1
#define JackPortIsOutput 0x2
#define JackPortIsPhysical 0x4
#define JACK_DEFAULT_AUDIO_TYPE "32 bit float mono audio"

#define AMPSIM_MAX_IN 8

static void *lib;
static jack_client_t *(*j_client_open)(const char *, int, int *, ...);
static int (*j_client_close)(jack_client_t *);
static int (*j_set_process_callback)(jack_client_t *, JackProcessCallback, void *);
static void (*j_on_shutdown)(jack_client_t *, JackShutdownCallback, void *);
static int (*j_activate)(jack_client_t *);
static int (*j_deactivate)(jack_client_t *);
static jack_port_t *(*j_port_register)(jack_client_t *, const char *, const char *, unsigned long, unsigned long);
static void *(*j_port_get_buffer)(jack_port_t *, jack_nframes_t);
static const char *(*j_port_name)(const jack_port_t *);
static const char **(*j_get_ports)(jack_client_t *, const char *, const char *, unsigned long);
static int (*j_connect)(jack_client_t *, const char *, const char *);
static jack_nframes_t (*j_get_sample_rate)(jack_client_t *);
static jack_nframes_t (*j_get_buffer_size)(jack_client_t *);
static void (*j_free)(void *);

static jack_client_t *client;
static jack_port_t *inports[AMPSIM_MAX_IN];
static int nin;
static jack_port_t *outports[2];
static volatile int shutdown_flag;

#define LOAD(var, name)                                   \
	do {                                                  \
		*(void **)(&var) = dlsym(lib, name);              \
		if (!var) {                                       \
			dlclose(lib);                                 \
			lib = NULL;                                   \
			return -2;                                    \
		}                                                 \
	} while (0)

// Carrega a libjack de path. 0 = ok; -1 = não abriu; -2 = símbolo faltando.
int ampsim_jack_load(const char *path) {
	if (lib) {
		return 0;
	}
	lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (!lib) {
		return -1;
	}
	LOAD(j_client_open, "jack_client_open");
	LOAD(j_client_close, "jack_client_close");
	LOAD(j_set_process_callback, "jack_set_process_callback");
	LOAD(j_on_shutdown, "jack_on_shutdown");
	LOAD(j_activate, "jack_activate");
	LOAD(j_deactivate, "jack_deactivate");
	LOAD(j_port_register, "jack_port_register");
	LOAD(j_port_get_buffer, "jack_port_get_buffer");
	LOAD(j_port_name, "jack_port_name");
	LOAD(j_get_ports, "jack_get_ports");
	LOAD(j_connect, "jack_connect");
	LOAD(j_get_sample_rate, "jack_get_sample_rate");
	LOAD(j_get_buffer_size, "jack_get_buffer_size");
	LOAD(j_free, "jack_free");
	return 0;
}

static int process(jack_nframes_t n, void *arg) {
	float *ins[AMPSIM_MAX_IN];
	for (int i = 0; i < nin; i++) {
		ins[i] = (float *)j_port_get_buffer(inports[i], n);
	}
	float *l = (float *)j_port_get_buffer(outports[0], n);
	float *r = (float *)j_port_get_buffer(outports[1], n);
	ampsimJackProcess(ins, nin, l, r, n);
	return 0;
}

static void on_shutdown(void *arg) { shutdown_flag = 1; }

// Abre o cliente e registra nIn portas de entrada e 2 de saída (sem ativar).
int ampsim_jack_open(const char *name, int nIn) {
	if (!lib || client) {
		return -1;
	}
	int status = 0;
	client = j_client_open(name, JackNoStartServer, &status);
	if (!client) {
		return -2;
	}
	shutdown_flag = 0;
	nin = nIn > AMPSIM_MAX_IN ? AMPSIM_MAX_IN : nIn;
	char pname[32];
	for (int i = 0; i < nin; i++) {
		snprintf(pname, sizeof pname, "in_%d", i + 1);
		inports[i] = j_port_register(client, pname, JACK_DEFAULT_AUDIO_TYPE, JackPortIsInput, 0);
		if (!inports[i]) {
			return -3;
		}
	}
	outports[0] = j_port_register(client, "out_L", JACK_DEFAULT_AUDIO_TYPE, JackPortIsOutput, 0);
	outports[1] = j_port_register(client, "out_R", JACK_DEFAULT_AUDIO_TYPE, JackPortIsOutput, 0);
	if (!outports[0] || !outports[1]) {
		return -3;
	}
	j_set_process_callback(client, process, NULL);
	j_on_shutdown(client, on_shutdown, NULL);
	return 0;
}

int ampsim_jack_activate(void) { return client ? j_activate(client) : -1; }

void ampsim_jack_close(void) {
	if (!client) {
		return;
	}
	j_deactivate(client); // para os callbacks antes de fechar
	j_client_close(client);
	client = NULL;
	nin = 0;
}

uint32_t ampsim_jack_sample_rate(void) { return client ? j_get_sample_rate(client) : 0; }
uint32_t ampsim_jack_buffer_size(void) { return client ? j_get_buffer_size(client) : 0; }
int ampsim_jack_is_shutdown(void) { return shutdown_flag; }

// Nome completo da nossa porta (out = 1: saída idx; out = 0: entrada idx).
const char *ampsim_jack_port_name(int out, int idx) {
	if (!client) {
		return NULL;
	}
	if (out) {
		return (idx >= 0 && idx < 2) ? j_port_name(outports[idx]) : NULL;
	}
	return (idx >= 0 && idx < nin) ? j_port_name(inports[idx]) : NULL;
}

int ampsim_jack_connect(const char *src, const char *dst) { return client ? j_connect(client, src, dst) : -1; }

// Lista as portas físicas: capture = 1 → portas que entregam áudio (entradas das placas);
// capture = 0 → portas que recebem áudio (saídas das placas). Sem cliente aberto, abre um
// temporário só para listar. Devolve uma cópia (os nomes do JACK somem quando o cliente
// fecha), terminada em NULL, liberada com ampsim_jack_free_ports.
char **ampsim_jack_list(int capture) {
	if (!lib) {
		return NULL;
	}
	jack_client_t *c = client;
	int temp = 0;
	if (!c) {
		int status = 0;
		c = j_client_open("ampsim-lista", JackNoStartServer, &status);
		if (!c) {
			return NULL;
		}
		temp = 1;
	}
	unsigned long flags = JackPortIsPhysical | (capture ? JackPortIsOutput : JackPortIsInput);
	const char **ports = j_get_ports(c, NULL, JACK_DEFAULT_AUDIO_TYPE, flags);
	int n = 0;
	while (ports && ports[n]) {
		n++;
	}
	char **copy = calloc(n + 1, sizeof(char *));
	for (int i = 0; copy && i < n; i++) {
		copy[i] = strdup(ports[i]);
	}
	if (ports) {
		j_free((void *)ports);
	}
	if (temp) {
		j_client_close(c);
	}
	return copy;
}

void ampsim_jack_free_ports(char **ports) {
	if (!ports) {
		return;
	}
	for (int i = 0; ports[i]; i++) {
		free(ports[i]);
	}
	free(ports);
}
