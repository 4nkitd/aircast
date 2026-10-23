//go:build windows && cgo

#define COBJMACROS
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#include <windows.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <mmreg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "wasapi_windows.h"

#define AC_MAX_PACKET_BYTES (4u * 1024u * 1024u)

static const GUID ac_device_enumerator = {0xbcde0395, 0xe52f, 0x467c, {0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}};
static const GUID ac_iid_enumerator = {0xa95664d2, 0x9614, 0x4f35, {0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}};
static const GUID ac_iid_client = {0x1cb9ad4c, 0xdbfa, 0x4c32, {0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}};
static const GUID ac_iid_capture = {0xc8adbd64, 0xe71e, 0x48a0, {0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}};
static const GUID ac_subformat_pcm = {1, 0, 0x0010, {0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}};
static const GUID ac_subformat_float = {3, 0, 0x0010, {0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}};

struct ac_wasapi {
    IAudioClient *client;
    IAudioCaptureClient *capture;
    uint32_t block_align;
    uint32_t buffer_frames;
    int initialized_com;
    int started;
};

static int ac_error(char *error, size_t size, const char *operation, HRESULT hr) {
    if (size > 0) {
        snprintf(error, size, "%s (HRESULT 0x%08x)", operation, (unsigned int)hr);
    }
    return -1;
}

void ac_wasapi_close(ac_wasapi *capture) {
    if (!capture) return;
    if (capture->started) IAudioClient_Stop(capture->client);
    if (capture->capture) IAudioCaptureClient_Release(capture->capture);
    if (capture->client) IAudioClient_Release(capture->client);
    if (capture->initialized_com) CoUninitialize();
    free(capture);
}

int ac_wasapi_open(ac_wasapi **out, ac_wasapi_format *format, char *error, size_t error_size) {
    HRESULT hr;
    const char *operation = "allocate WASAPI state";
    IMMDeviceEnumerator *enumerator = NULL;
    IMMDevice *device = NULL;
    WAVEFORMATEX *mix = NULL;
    ac_wasapi *capture = calloc(1, sizeof(*capture));
    *out = NULL;
    memset(format, 0, sizeof(*format));
    if (!capture) return ac_error(error, error_size, operation, E_OUTOFMEMORY);

    operation = "CoInitializeEx for WASAPI";
    hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (FAILED(hr)) goto failed;
    capture->initialized_com = 1;
    operation = "create Windows audio endpoint enumerator";
    hr = CoCreateInstance(&ac_device_enumerator, NULL, CLSCTX_INPROC_SERVER,
                          &ac_iid_enumerator, (void **)&enumerator);
    if (FAILED(hr)) goto failed;
    operation = "get default system-output endpoint, not a microphone";
    hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(enumerator, eRender, eConsole, &device);
    if (FAILED(hr)) goto failed;
    operation = "activate default output IAudioClient";
    hr = IMMDevice_Activate(device, &ac_iid_client, CLSCTX_INPROC_SERVER, NULL, (void **)&capture->client);
    if (FAILED(hr)) goto failed;
    operation = "get default output mix format";
    hr = IAudioClient_GetMixFormat(capture->client, &mix);
    if (FAILED(hr)) goto failed;

    operation = "validate bounded WASAPI mix format";
    hr = AUDCLNT_E_UNSUPPORTED_FORMAT;
    if (!mix || mix->nSamplesPerSec < 8000 || mix->nSamplesPerSec > 192000 ||
        mix->nChannels < 1 || mix->nChannels > 8) goto failed;
    format->rate = mix->nSamplesPerSec;
    format->channels = mix->nChannels;
    format->bits = mix->wBitsPerSample;
    format->valid_bits = mix->wBitsPerSample;
    format->block_align = mix->nBlockAlign;
    if (mix->wFormatTag == WAVE_FORMAT_EXTENSIBLE) {
        if (mix->cbSize < 22) goto failed;
        const WAVEFORMATEXTENSIBLE *extended = (const WAVEFORMATEXTENSIBLE *)mix;
        format->valid_bits = extended->Samples.wValidBitsPerSample;
        format->channel_mask = extended->dwChannelMask;
        if (!memcmp(&extended->SubFormat, &ac_subformat_float, sizeof(GUID))) {
            format->floating = 1;
        } else if (memcmp(&extended->SubFormat, &ac_subformat_pcm, sizeof(GUID))) {
            goto failed;
        }
    } else if (mix->wFormatTag == WAVE_FORMAT_IEEE_FLOAT) {
        format->floating = 1;
    } else if (mix->wFormatTag != WAVE_FORMAT_PCM) {
        goto failed;
    }
    if (format->floating) {
        if ((format->bits != 32 && format->bits != 64) || format->valid_bits != format->bits) goto failed;
    } else if (format->bits != 8 && format->bits != 16 && format->bits != 24 && format->bits != 32) {
        goto failed;
    }
    if (!format->valid_bits || format->valid_bits > format->bits ||
        format->block_align != format->channels * (format->bits / 8) ||
        mix->nAvgBytesPerSec != format->rate * format->block_align) goto failed;

    operation = "initialize shared-mode output WASAPI loopback";
    hr = IAudioClient_Initialize(capture->client, AUDCLNT_SHAREMODE_SHARED,
                                 AUDCLNT_STREAMFLAGS_LOOPBACK, 1000000, 0, mix, NULL);
    if (FAILED(hr)) goto failed;
    operation = "get bounded WASAPI loopback buffer";
    hr = IAudioClient_GetBufferSize(capture->client, &capture->buffer_frames);
    if (FAILED(hr)) goto failed;
    if (!capture->buffer_frames || capture->buffer_frames > format->rate / 4 ||
        capture->buffer_frames > AC_MAX_PACKET_BYTES / format->block_align) {
        hr = E_INVALIDARG;
        goto failed;
    }
    format->buffer_frames = capture->buffer_frames;
    capture->block_align = format->block_align;
    operation = "get WASAPI loopback capture service";
    hr = IAudioClient_GetService(capture->client, &ac_iid_capture, (void **)&capture->capture);
    if (FAILED(hr)) goto failed;
    operation = "start default-output WASAPI loopback";
    hr = IAudioClient_Start(capture->client);
    if (FAILED(hr)) goto failed;
    capture->started = 1;
    CoTaskMemFree(mix);
    IMMDevice_Release(device);
    IMMDeviceEnumerator_Release(enumerator);
    *out = capture;
    return 0;

failed:
    if (mix) CoTaskMemFree(mix);
    if (device) IMMDevice_Release(device);
    if (enumerator) IMMDeviceEnumerator_Release(enumerator);
    ac_wasapi_close(capture);
    return ac_error(error, error_size, operation, hr);
}

int ac_wasapi_read(ac_wasapi *capture, unsigned char *data, size_t capacity,
                   ac_wasapi_packet *packet, char *error, size_t error_size) {
    UINT32 available = 0;
    HRESULT hr = IAudioCaptureClient_GetNextPacketSize(capture->capture, &available);
    if (FAILED(hr)) return ac_error(error, error_size, "query output loopback packet", hr);
    if (!available) return 0;
    if (available > capture->buffer_frames || available > capacity / capture->block_align) {
        return ac_error(error, error_size, "WASAPI packet exceeds bounded capture buffer", E_INVALIDARG);
    }
    BYTE *source = NULL;
    UINT32 frames = 0;
    DWORD flags = 0;
    UINT64 position = 0, qpc = 0;
    hr = IAudioCaptureClient_GetBuffer(capture->capture, &source, &frames, &flags, &position, &qpc);
    if (hr == AUDCLNT_S_BUFFER_EMPTY) return 0;
    if (FAILED(hr)) return ac_error(error, error_size, "read output loopback packet", hr);
    int invalid = frames == 0 || frames > capture->buffer_frames || frames > capacity / capture->block_align;
    if (!invalid && !(flags & AUDCLNT_BUFFERFLAGS_SILENT)) {
        if (!source) invalid = 1;
        else memcpy(data, source, (size_t)frames * capture->block_align);
    }
    HRESULT release_hr = IAudioCaptureClient_ReleaseBuffer(capture->capture, frames);
    if (FAILED(release_hr)) return ac_error(error, error_size, "release output loopback packet", release_hr);
    if (invalid) return ac_error(error, error_size, "invalid or oversized output loopback packet", E_INVALIDARG);
    packet->frames = frames;
    packet->flags = flags;
    packet->device_position = position;
    packet->qpc_100ns = qpc;
    return 1;
}

uint64_t ac_wasapi_qpc_100ns(void) {
    LARGE_INTEGER value, frequency;
    if (!QueryPerformanceCounter(&value) || !QueryPerformanceFrequency(&frequency) || frequency.QuadPart <= 0) return 0;
    return (uint64_t)(value.QuadPart / frequency.QuadPart) * 10000000u +
           (uint64_t)(value.QuadPart % frequency.QuadPart) * 10000000u / (uint64_t)frequency.QuadPart;
}
