/*
 * lv2min.h: as poucas definições da API LV2 que o plugin usa (core, urid, state), escritas
 * a partir da especificação (https://lv2plug.in, licença ISC), para o build não depender
 * do pacote lv2-dev. O ABI é o da especificação: não mudar a ordem dos campos.
 */
#ifndef AMPSIM_LV2MIN_H
#define AMPSIM_LV2MIN_H

#include <stddef.h>
#include <stdint.h>

#define LV2_URID__map "http://lv2plug.in/ns/ext/urid#map"
#define LV2_STATE__interface "http://lv2plug.in/ns/ext/state#interface"
#define LV2_ATOM__String "http://lv2plug.in/ns/ext/atom#String"

typedef void *LV2_Handle;

typedef struct {
	const char *URI;
	void *data;
} LV2_Feature;

typedef struct LV2_Descriptor {
	const char *URI;
	LV2_Handle (*instantiate)(const struct LV2_Descriptor *descriptor, double sample_rate,
	                          const char *bundle_path, const LV2_Feature *const *features);
	void (*connect_port)(LV2_Handle instance, uint32_t port, void *data_location);
	void (*activate)(LV2_Handle instance);
	void (*run)(LV2_Handle instance, uint32_t sample_count);
	void (*deactivate)(LV2_Handle instance);
	void (*cleanup)(LV2_Handle instance);
	const void *(*extension_data)(const char *uri);
} LV2_Descriptor;

typedef uint32_t LV2_URID;
typedef void *LV2_URID_Map_Handle;

typedef struct {
	LV2_URID_Map_Handle handle;
	LV2_URID (*map)(LV2_URID_Map_Handle handle, const char *uri);
} LV2_URID_Map;

typedef void *LV2_State_Handle;

typedef enum {
	LV2_STATE_IS_POD = 1,
	LV2_STATE_IS_PORTABLE = 1 << 1,
	LV2_STATE_IS_NATIVE = 1 << 2,
} LV2_State_Flags;

typedef enum {
	LV2_STATE_SUCCESS = 0,
	LV2_STATE_ERR_UNKNOWN = 1,
	LV2_STATE_ERR_BAD_TYPE = 2,
	LV2_STATE_ERR_BAD_FLAGS = 3,
	LV2_STATE_ERR_NO_FEATURE = 4,
	LV2_STATE_ERR_NO_PROPERTY = 5,
	LV2_STATE_ERR_NO_SPACE = 6,
} LV2_State_Status;

typedef LV2_State_Status (*LV2_State_Store_Function)(LV2_State_Handle handle, uint32_t key,
                                                     const void *value, size_t size, uint32_t type,
                                                     uint32_t flags);

typedef const void *(*LV2_State_Retrieve_Function)(LV2_State_Handle handle, uint32_t key,
                                                   size_t *size, uint32_t *type, uint32_t *flags);

typedef struct {
	LV2_State_Status (*save)(LV2_Handle instance, LV2_State_Store_Function store,
	                         LV2_State_Handle handle, uint32_t flags,
	                         const LV2_Feature *const *features);
	LV2_State_Status (*restore)(LV2_Handle instance, LV2_State_Retrieve_Function retrieve,
	                            LV2_State_Handle handle, uint32_t flags,
	                            const LV2_Feature *const *features);
} LV2_State_Interface;

#endif
