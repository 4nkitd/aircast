#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <dxgi1_6.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "native.h"

typedef DPI_AWARENESS_CONTEXT (WINAPI *SetThreadDPI)(DPI_AWARENESS_CONTEXT);

struct AircastCapture {
    AircastMonitor monitor;
    SetThreadDPI set_dpi;
    DPI_AWARENESS_CONTEXT old_dpi;
    ID3D11Device *device;
    ID3D11DeviceContext *context;
    IDXGIOutputDuplication *duplication;
    ID3D11Texture2D *staging;
    HDC screen_dc, memory_dc;
    HBITMAP bitmap;
    HGDIOBJ old_bitmap;
    unsigned char *dib;
    int cursor, gdi, have_frame, pointer_visible;
};

static void message(char *error, size_t size, const char *operation, HRESULT result) {
    snprintf(error, size, "%s (0x%08lx)", operation, (unsigned long)result);
}

static SetThreadDPI dpi_function(void) {
    union { FARPROC generic; SetThreadDPI typed; } function;
    function.generic = GetProcAddress(GetModuleHandleW(L"user32.dll"), "SetThreadDpiAwarenessContext");
    return function.typed;
}

static int interactive_desktop(char *error, size_t size) {
    HDESK desktop = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
    wchar_t name[128];
    DWORD needed = 0;
    if (!desktop) {
        message(error, size, "interactive desktop is unavailable or locked", HRESULT_FROM_WIN32(GetLastError()));
        return 0;
    }
    int valid = GetUserObjectInformationW(desktop, UOI_NAME, name, sizeof(name), &needed) && _wcsicmp(name, L"Default") == 0;
    CloseDesktop(desktop);
    if (!valid) snprintf(error, size, "capture stops outside the user's default interactive desktop");
    return valid;
}

static int sdr_display(const DXGI_OUTPUT_DESC *desc, IDXGIOutput *output, char *error, size_t size) {
    IDXGIOutput6 *output6 = NULL;
    HRESULT hr = IDXGIOutput_QueryInterface(output, &IID_IDXGIOutput6, (void **)&output6);
    if (SUCCEEDED(hr)) {
        DXGI_OUTPUT_DESC1 state;
        hr = IDXGIOutput6_GetDesc1(output6, &state);
        IDXGIOutput6_Release(output6);
        if (SUCCEEDED(hr)) {
            if (state.ColorSpace == DXGI_COLOR_SPACE_RGB_FULL_G22_NONE_P709) return 1;
            snprintf(error, size, "HDR or wide-color display is active; select an SDR display or disable HDR manually");
            return 0;
        }
    }
    UINT32 path_count = 0, mode_count = 0;
    LONG result = GetDisplayConfigBufferSizes(QDC_ONLY_ACTIVE_PATHS, &path_count, &mode_count);
    if (result != ERROR_SUCCESS || path_count == 0 || path_count > 256 || mode_count > 1024) {
        snprintf(error, size, "cannot establish the display's SDR color state");
        return 0;
    }
    DISPLAYCONFIG_PATH_INFO *paths = calloc(path_count, sizeof(*paths));
    DISPLAYCONFIG_MODE_INFO *modes = calloc(mode_count, sizeof(*modes));
    if (!paths || !modes) {
        free(paths); free(modes);
        snprintf(error, size, "allocate display color metadata");
        return 0;
    }
    result = QueryDisplayConfig(QDC_ONLY_ACTIVE_PATHS, &path_count, paths, &mode_count, modes, NULL);
    int verified = 0;
    for (UINT32 index = 0; result == ERROR_SUCCESS && index < path_count; index++) {
        DISPLAYCONFIG_SOURCE_DEVICE_NAME source;
        memset(&source, 0, sizeof(source));
        source.header.type = DISPLAYCONFIG_DEVICE_INFO_GET_SOURCE_NAME;
        source.header.size = sizeof(source);
        source.header.adapterId = paths[index].sourceInfo.adapterId;
        source.header.id = paths[index].sourceInfo.id;
        if (DisplayConfigGetDeviceInfo(&source.header) != ERROR_SUCCESS || _wcsicmp(source.viewGdiDeviceName, desc->DeviceName) != 0) continue;
        DISPLAYCONFIG_GET_ADVANCED_COLOR_INFO state;
        memset(&state, 0, sizeof(state));
        state.header.type = DISPLAYCONFIG_DEVICE_INFO_GET_ADVANCED_COLOR_INFO;
        state.header.size = sizeof(state);
        state.header.adapterId = paths[index].targetInfo.adapterId;
        state.header.id = paths[index].targetInfo.id;
        if (DisplayConfigGetDeviceInfo(&state.header) == ERROR_SUCCESS && !state.advancedColorEnabled && !state.wideColorEnforced) verified = 1;
        break;
    }
    free(paths); free(modes);
    if (!verified) snprintf(error, size, "display is HDR/wide-color or its SDR state is unknown; use a verified SDR display");
    return verified;
}

static int find_primary(AircastMonitor *info, IDXGIAdapter1 **selected_adapter, IDXGIOutput **selected_output, char *error, size_t size) {
    if (!interactive_desktop(error, size)) return 0;
    POINT point = {0, 0};
    HMONITOR monitor = MonitorFromPoint(point, MONITOR_DEFAULTTOPRIMARY);
    IDXGIFactory1 *factory = NULL;
    HRESULT hr = CreateDXGIFactory1(&IID_IDXGIFactory1, (void **)&factory);
    if (FAILED(hr)) {
        message(error, size, "create DXGI factory", hr);
        return 0;
    }
    for (UINT adapter_index = 0; ; adapter_index++) {
        IDXGIAdapter1 *adapter = NULL;
        hr = IDXGIFactory1_EnumAdapters1(factory, adapter_index, &adapter);
        if (hr == DXGI_ERROR_NOT_FOUND) break;
        if (FAILED(hr)) break;
        for (UINT output_index = 0; ; output_index++) {
            IDXGIOutput *output = NULL;
            hr = IDXGIAdapter1_EnumOutputs(adapter, output_index, &output);
            if (hr == DXGI_ERROR_NOT_FOUND) break;
            if (FAILED(hr)) break;
            DXGI_OUTPUT_DESC desc;
            hr = IDXGIOutput_GetDesc(output, &desc);
            if (FAILED(hr) || !desc.AttachedToDesktop || desc.Monitor != monitor) {
                IDXGIOutput_Release(output);
                continue;
            }
            if (desc.Rotation != DXGI_MODE_ROTATION_IDENTITY || !sdr_display(&desc, output, error, size)) {
                if (desc.Rotation != DXGI_MODE_ROTATION_IDENTITY) snprintf(error, size, "rotated primary monitors are not supported by the initial DXGI capture path");
                IDXGIOutput_Release(output);
                IDXGIAdapter1_Release(adapter);
                IDXGIFactory1_Release(factory);
                return 0;
            }
            DXGI_ADAPTER_DESC1 adapter_desc;
            hr = IDXGIAdapter1_GetDesc1(adapter, &adapter_desc);
            if (FAILED(hr)) {
                message(error, size, "read capture adapter identity", hr);
                IDXGIOutput_Release(output);
                IDXGIAdapter1_Release(adapter);
                IDXGIFactory1_Release(factory);
                return 0;
            }
            memset(info, 0, sizeof(*info));
            info->x = desc.DesktopCoordinates.left;
            info->y = desc.DesktopCoordinates.top;
            info->width = desc.DesktopCoordinates.right - desc.DesktopCoordinates.left;
            info->height = desc.DesktopCoordinates.bottom - desc.DesktopCoordinates.top;
            info->adapter_low = adapter_desc.AdapterLuid.LowPart;
            info->adapter_high = adapter_desc.AdapterLuid.HighPart;
            info->adapter_index = adapter_index;
            info->output_index = output_index;
            info->monitor = (uintptr_t)monitor;
            *selected_adapter = adapter;
            *selected_output = output;
            IDXGIFactory1_Release(factory);
            return 1;
        }
        IDXGIAdapter1_Release(adapter);
    }
    IDXGIFactory1_Release(factory);
    snprintf(error, size, "no active DXGI output matches the primary monitor");
    return 0;
}

int aircast_primary_monitor(AircastMonitor *monitor, char *error, size_t size) {
    SetThreadDPI set_dpi = dpi_function();
    if (!set_dpi) {
        snprintf(error, size, "Per-Monitor V2 DPI awareness requires Windows 10 version 1703 or newer");
        return 0;
    }
    DPI_AWARENESS_CONTEXT old_dpi = set_dpi(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
    if (!old_dpi) {
        message(error, size, "set capture thread DPI awareness", HRESULT_FROM_WIN32(GetLastError()));
        return 0;
    }
    IDXGIAdapter1 *adapter = NULL;
    IDXGIOutput *output = NULL;
    int result = find_primary(monitor, &adapter, &output, error, size);
    if (output) IDXGIOutput_Release(output);
    if (adapter) IDXGIAdapter1_Release(adapter);
    set_dpi(old_dpi);
    return result;
}

static void release_dxgi(AircastCapture *capture) {
    if (capture->staging) ID3D11Texture2D_Release(capture->staging);
    if (capture->duplication) IDXGIOutputDuplication_Release(capture->duplication);
    if (capture->context) ID3D11DeviceContext_Release(capture->context);
    if (capture->device) ID3D11Device_Release(capture->device);
    capture->staging = NULL; capture->duplication = NULL;
    capture->context = NULL; capture->device = NULL;
}

AircastCapture *aircast_capture_open(const AircastMonitor *monitor, int cursor, char *error, size_t size) {
    if (!cursor) {
        snprintf(error, size, "DXGI/GDI cannot guarantee cursor exclusion; enable cursor sharing");
        return NULL;
    }
    AircastCapture *capture = calloc(1, sizeof(*capture));
    if (!capture) {
        snprintf(error, size, "allocate native capture session");
        return NULL;
    }
    capture->set_dpi = dpi_function();
    if (!capture->set_dpi || !(capture->old_dpi = capture->set_dpi(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2))) {
        snprintf(error, size, "cannot set capture thread Per-Monitor V2 DPI context");
        free(capture);
        return NULL;
    }
    IDXGIAdapter1 *adapter = NULL;
    IDXGIOutput *output = NULL;
    AircastMonitor current;
    if (!find_primary(&current, &adapter, &output, error, size)) {
        aircast_capture_close(capture);
        return NULL;
    }
    if (monitor->monitor != current.monitor || monitor->adapter_low != current.adapter_low || monitor->adapter_high != current.adapter_high ||
        monitor->adapter_index != current.adapter_index || monitor->output_index != current.output_index ||
        monitor->x != current.x || monitor->y != current.y || monitor->width != current.width || monitor->height != current.height ||
        current.width < 2 || current.height < 2 || current.width > 16384 || current.height > 16384) {
        snprintf(error, size, "primary display changed after capture preparation; start sharing again");
        IDXGIOutput_Release(output); IDXGIAdapter1_Release(adapter);
        aircast_capture_close(capture);
        return NULL;
    }
    capture->monitor = current;
    capture->cursor = cursor;
    capture->screen_dc = GetDC(NULL);
    capture->memory_dc = CreateCompatibleDC(capture->screen_dc);
    BITMAPINFO bitmap;
    memset(&bitmap, 0, sizeof(bitmap));
    bitmap.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
    bitmap.bmiHeader.biWidth = current.width;
    bitmap.bmiHeader.biHeight = -current.height;
    bitmap.bmiHeader.biPlanes = 1;
    bitmap.bmiHeader.biBitCount = 32;
    bitmap.bmiHeader.biCompression = BI_RGB;
    capture->bitmap = CreateDIBSection(capture->screen_dc, &bitmap, DIB_RGB_COLORS, (void **)&capture->dib, NULL, 0);
    if (!capture->screen_dc || !capture->memory_dc || !capture->bitmap || !capture->dib) {
        message(error, size, "allocate screen staging bitmap", HRESULT_FROM_WIN32(GetLastError()));
        IDXGIOutput_Release(output); IDXGIAdapter1_Release(adapter);
        aircast_capture_close(capture);
        return NULL;
    }
    capture->old_bitmap = SelectObject(capture->memory_dc, capture->bitmap);
    if (!capture->old_bitmap || capture->old_bitmap == HGDI_ERROR) {
        capture->old_bitmap = NULL;
        message(error, size, "select native capture staging bitmap", HRESULT_FROM_WIN32(GetLastError()));
        IDXGIOutput_Release(output); IDXGIAdapter1_Release(adapter);
        aircast_capture_close(capture);
        return NULL;
    }
    HRESULT hr = D3D11CreateDevice((IDXGIAdapter *)adapter, D3D_DRIVER_TYPE_UNKNOWN, NULL,
        D3D11_CREATE_DEVICE_BGRA_SUPPORT, NULL, 0, D3D11_SDK_VERSION, &capture->device, NULL, &capture->context);
    IDXGIOutput1 *output1 = NULL;
    if (SUCCEEDED(hr)) hr = IDXGIOutput_QueryInterface(output, &IID_IDXGIOutput1, (void **)&output1);
    if (SUCCEEDED(hr)) hr = IDXGIOutput1_DuplicateOutput(output1, (IUnknown *)capture->device, &capture->duplication);
    if (output1) IDXGIOutput1_Release(output1);
    IDXGIOutput_Release(output); IDXGIAdapter1_Release(adapter);
    if (FAILED(hr)) {
        if (hr == E_ACCESSDENIED) {
            message(error, size, "DXGI capture permission denied", hr);
            aircast_capture_close(capture);
            return NULL;
        }
        release_dxgi(capture);
        capture->gdi = 1;
    }
    return capture;
}

static void draw_cursor(AircastCapture *capture) {
    if (!capture->cursor || (!capture->gdi && !capture->pointer_visible)) return;
    CURSORINFO cursor;
    memset(&cursor, 0, sizeof(cursor));
    cursor.cbSize = sizeof(cursor);
    if (!GetCursorInfo(&cursor) || !(cursor.flags & CURSOR_SHOWING)) return;
    ICONINFO icon;
    memset(&icon, 0, sizeof(icon));
    if (!GetIconInfo(cursor.hCursor, &icon)) return;
    DrawIconEx(capture->memory_dc, cursor.ptScreenPos.x - capture->monitor.x - (int)icon.xHotspot,
        cursor.ptScreenPos.y - capture->monitor.y - (int)icon.yHotspot, cursor.hCursor, 0, 0, 0, NULL, DI_NORMAL);
    if (icon.hbmMask) DeleteObject(icon.hbmMask);
    if (icon.hbmColor) DeleteObject(icon.hbmColor);
}

int aircast_capture_frame(AircastCapture *capture, unsigned char *pixels, size_t length, char *error, size_t size) {
    size_t row_bytes = (size_t)capture->monitor.width * 4;
    if (length != row_bytes * (size_t)capture->monitor.height) {
        snprintf(error, size, "screen pixel buffer size mismatch");
        return -1;
    }
    if (!interactive_desktop(error, size)) return -1;
    if (capture->gdi) {
        if (!BitBlt(capture->memory_dc, 0, 0, capture->monitor.width, capture->monitor.height,
            capture->screen_dc, capture->monitor.x, capture->monitor.y, SRCCOPY | CAPTUREBLT)) {
            message(error, size, "GDI compatibility capture", HRESULT_FROM_WIN32(GetLastError()));
            return -1;
        }
    } else {
        DXGI_OUTDUPL_FRAME_INFO info;
        IDXGIResource *resource = NULL;
        HRESULT hr = IDXGIOutputDuplication_AcquireNextFrame(capture->duplication, 0, &info, &resource);
        if (hr != DXGI_ERROR_WAIT_TIMEOUT && FAILED(hr)) {
            message(error, size, "DXGI access/device lost; start sharing again", hr);
            return -1;
        }
        if (hr == DXGI_ERROR_WAIT_TIMEOUT && !capture->have_frame) return 0;
        if (SUCCEEDED(hr)) {
            ID3D11Texture2D *texture = NULL;
            hr = IDXGIResource_QueryInterface(resource, &IID_ID3D11Texture2D, (void **)&texture);
            IDXGIResource_Release(resource);
            if (SUCCEEDED(hr)) {
                D3D11_TEXTURE2D_DESC desc;
                ID3D11Texture2D_GetDesc(texture, &desc);
                if (desc.Width != (UINT)capture->monitor.width || desc.Height != (UINT)capture->monitor.height || desc.Format != DXGI_FORMAT_B8G8R8A8_UNORM) {
                    hr = E_UNEXPECTED;
                } else if (!capture->staging) {
                    desc.Usage = D3D11_USAGE_STAGING;
                    desc.BindFlags = 0;
                    desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
                    desc.MiscFlags = 0;
                    hr = ID3D11Device_CreateTexture2D(capture->device, &desc, NULL, &capture->staging);
                }
                if (SUCCEEDED(hr)) ID3D11DeviceContext_CopyResource(capture->context, (ID3D11Resource *)capture->staging, (ID3D11Resource *)texture);
                ID3D11Texture2D_Release(texture);
            }
            HRESULT release_hr = IDXGIOutputDuplication_ReleaseFrame(capture->duplication);
            if (SUCCEEDED(hr)) hr = release_hr;
            if (FAILED(hr)) {
                message(error, size, "copy physical DXGI frame", hr);
                return -1;
            }
            if (info.LastMouseUpdateTime.QuadPart != 0) capture->pointer_visible = info.PointerPosition.Visible;
            capture->have_frame = 1;
        }
        D3D11_MAPPED_SUBRESOURCE mapped;
        hr = ID3D11DeviceContext_Map(capture->context, (ID3D11Resource *)capture->staging, 0, D3D11_MAP_READ, 0, &mapped);
        if (FAILED(hr)) {
            message(error, size, "map DXGI readback", hr);
            return -1;
        }
        if (mapped.RowPitch < row_bytes) {
            ID3D11DeviceContext_Unmap(capture->context, (ID3D11Resource *)capture->staging, 0);
            snprintf(error, size, "DXGI frame stride is smaller than its physical width");
            return -1;
        }
        for (int y = 0; y < capture->monitor.height; y++) {
            memcpy(capture->dib + (size_t)y * row_bytes, (unsigned char *)mapped.pData + (size_t)y * mapped.RowPitch, row_bytes);
        }
        ID3D11DeviceContext_Unmap(capture->context, (ID3D11Resource *)capture->staging, 0);
    }
    draw_cursor(capture);
    GdiFlush();
    memcpy(pixels, capture->dib, length);
    return 1;
}

int aircast_capture_is_gdi(AircastCapture *capture) { return capture->gdi; }

void aircast_capture_close(AircastCapture *capture) {
    if (!capture) return;
    release_dxgi(capture);
    if (capture->old_bitmap && capture->memory_dc) SelectObject(capture->memory_dc, capture->old_bitmap);
    if (capture->bitmap) DeleteObject(capture->bitmap);
    if (capture->memory_dc) DeleteDC(capture->memory_dc);
    if (capture->screen_dc) ReleaseDC(NULL, capture->screen_dc);
    if (capture->set_dpi && capture->old_dpi) capture->set_dpi(capture->old_dpi);
    free(capture);
}
