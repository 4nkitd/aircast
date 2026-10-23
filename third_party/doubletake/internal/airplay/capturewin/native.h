#ifndef AIRCAST_CAPTURE_NATIVE_H
#define AIRCAST_CAPTURE_NATIVE_H

#include <stddef.h>
#include <stdint.h>

typedef struct {
    int x, y, width, height;
    uint32_t adapter_low;
    int32_t adapter_high;
    unsigned int adapter_index, output_index;
    uintptr_t monitor;
} AircastMonitor;

typedef struct AircastCapture AircastCapture;

int aircast_primary_monitor(AircastMonitor *monitor, char *error, size_t error_size);
AircastCapture *aircast_capture_open(const AircastMonitor *monitor, int cursor, char *error, size_t error_size);
int aircast_capture_frame(AircastCapture *capture, unsigned char *pixels, size_t length, char *error, size_t error_size);
int aircast_capture_is_gdi(AircastCapture *capture);
void aircast_capture_close(AircastCapture *capture);

#endif
