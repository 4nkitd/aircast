#ifndef AIRCAST_WASAPI_H
#define AIRCAST_WASAPI_H

#include <stddef.h>
#include <stdint.h>

typedef struct ac_wasapi ac_wasapi;

typedef struct {
    uint32_t rate;
    uint32_t channels;
    uint32_t bits;
    uint32_t valid_bits;
    uint32_t block_align;
    uint32_t floating;
    uint32_t channel_mask;
    uint32_t buffer_frames;
} ac_wasapi_format;

typedef struct {
    uint32_t frames;
    uint32_t flags;
    uint64_t device_position;
    uint64_t qpc_100ns;
} ac_wasapi_packet;

int ac_wasapi_open(ac_wasapi **out, ac_wasapi_format *format, char *error, size_t error_size);
int ac_wasapi_read(ac_wasapi *capture, unsigned char *data, size_t capacity,
                   ac_wasapi_packet *packet, char *error, size_t error_size);
uint64_t ac_wasapi_qpc_100ns(void);
void ac_wasapi_close(ac_wasapi *capture);

#endif
