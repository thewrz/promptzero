#include <furi.h>
#include <furi_hal.h>
#include <furi_hal_usb.h>
#include <furi_hal_usb_cdc.h>
#include <furi_hal_serial.h>
#include <furi_hal_serial_control.h>
#include <gui/gui.h>
#include <gui/view_port.h>
#include <cli/cli_vcp.h>
#include <expansion/expansion.h>

#include "bridge_policy.h"

#define BRIDGE_BAUD              115200
#define BRIDGE_UART              FuriHalSerialIdUsart
#define BRIDGE_VCP_CH            0
#define BRIDGE_STREAM_SIZE       512
#define BRIDGE_WORKER_STACK      1024
#define BRIDGE_CDC_TX_TIMEOUT_MS 1000
#define BRIDGE_STOP_POLL_MS      50

_Static_assert(
    BRIDGE_CDC_PACKET_MAX == CDC_DATA_SZ,
    "policy packet cap must match the CDC endpoint");

typedef struct {
    FuriThread* usb_to_uart_thread;
    FuriThread* uart_to_usb_thread;
    FuriStreamBuffer* usb_rx_stream;
    FuriStreamBuffer* uart_rx_stream;
    FuriHalSerialHandle* serial_handle;
    FuriSemaphore* tx_complete;
    CliVcp* cli_vcp;
    Expansion* expansion;
    BridgeLifecycle lifecycle;
    uint32_t usb_rx_dropped;
    uint32_t uart_rx_dropped;
    uint32_t uart_overrun_events;
    bool expansion_disabled;
    bool serial_initialized;
    bool serial_dma_started;
    bool cli_disabled;
    bool callbacks_installed;
    bool usb_to_uart_started;
    bool uart_to_usb_started;
} UsbUartApp;

static bool app_should_stop(UsbUartApp* app) {
    FURI_CRITICAL_ENTER();
    bool stopping = bridge_lifecycle_should_stop(&app->lifecycle);
    FURI_CRITICAL_EXIT();
    return stopping;
}

static void app_request_stop(UsbUartApp* app, BridgeStopReason reason) {
    FURI_CRITICAL_ENTER();
    bridge_lifecycle_request_stop(&app->lifecycle, reason);
    FURI_CRITICAL_EXIT();
}

static void app_record_loss(uint32_t* counter, size_t offered, size_t accepted) {
    FURI_CRITICAL_ENTER();
    *counter = bridge_loss_record(*counter, offered, accepted);
    FURI_CRITICAL_EXIT();
}

static void app_on_cdc_tx_complete(void* context) {
    UsbUartApp* app = context;
    (void)furi_semaphore_release(app->tx_complete);
}

static void app_on_cdc_rx(void* context) {
    UsbUartApp* app = context;
    if(app_should_stop(app)) return;

    uint8_t buffer[CDC_DATA_SZ];
    int32_t received = furi_hal_cdc_receive(BRIDGE_VCP_CH, buffer, sizeof(buffer));
    if(received <= 0) return;

    size_t offered = (size_t)received;
    size_t accepted = furi_stream_buffer_send(app->usb_rx_stream, buffer, offered, 0);
    app_record_loss(&app->usb_rx_dropped, offered, accepted);
}

static void app_on_cdc_line_config(void* context, struct usb_cdc_line_coding* config) {
    UNUSED(context);
    UNUSED(config);
}

static void app_on_cdc_state(void* context, CdcState state) {
    UsbUartApp* app = context;
    FURI_CRITICAL_ENTER();
    bridge_lifecycle_set_connected(&app->lifecycle, state == CdcStateConnected, furi_get_tick());
    FURI_CRITICAL_EXIT();
}

static void app_on_cdc_ctrl_line(void* context, CdcCtrlLine ctrl_lines) {
    UsbUartApp* app = context;
    bool dtr_high = (((uint8_t)ctrl_lines & (uint8_t)CdcCtrlLineDTR) != 0);
    FURI_CRITICAL_ENTER();
    bridge_lifecycle_set_dtr(&app->lifecycle, dtr_high, furi_get_tick());
    FURI_CRITICAL_EXIT();
}

static void app_on_serial_rx_dma(
    FuriHalSerialHandle* handle,
    FuriHalSerialRxEvent event,
    size_t data_len,
    void* context) {
    UsbUartApp* app = context;
    if(app_should_stop(app)) return;

    FURI_CRITICAL_ENTER();
    if(event & FuriHalSerialRxEventOverrunError) {
        app->uart_overrun_events = bridge_loss_record(app->uart_overrun_events, 1, 0);
    }
    FURI_CRITICAL_EXIT();

    // OFW reports short DMA bursts on Idle even when the Data bit is clear.
    size_t available = bridge_uart_rx_drain_length(
        (event & FuriHalSerialRxEventData) != 0,
        (event & FuriHalSerialRxEventIdle) != 0,
        data_len);
    if(available == 0) return;

    uint8_t buffer[FURI_HAL_SERIAL_DMA_BUFFER_SIZE];
    size_t requested = available < sizeof(buffer) ? available : sizeof(buffer);
    size_t received = furi_hal_serial_dma_rx(handle, buffer, requested);
    if(available > requested) app_record_loss(&app->uart_rx_dropped, available, requested);
    if(requested > received) app_record_loss(&app->uart_rx_dropped, requested, received);
    if(received == 0) return;

    size_t accepted = furi_stream_buffer_send(app->uart_rx_stream, buffer, received, 0);
    app_record_loss(&app->uart_rx_dropped, received, accepted);
}

static int32_t usb_to_uart_worker(void* context) {
    UsbUartApp* app = context;
    uint8_t buffer[CDC_DATA_SZ];

    while(!app_should_stop(app)) {
        size_t length = furi_stream_buffer_receive(
            app->usb_rx_stream, buffer, sizeof(buffer), BRIDGE_STOP_POLL_MS);
        if(length == 0 || app_should_stop(app)) continue;

        furi_hal_serial_tx(app->serial_handle, buffer, length);
        furi_hal_serial_tx_wait_complete(app->serial_handle);
    }

    return 0;
}

static int32_t uart_to_usb_worker(void* context) {
    UsbUartApp* app = context;
    uint8_t buffer[BRIDGE_STREAM_SIZE];

    while(!app_should_stop(app)) {
        size_t length = furi_stream_buffer_receive(
            app->uart_rx_stream, buffer, sizeof(buffer), BRIDGE_STOP_POLL_MS);
        if(length == 0) continue;

        BridgeTxCursor cursor = {0};
        while(cursor.offset < length && !app_should_stop(app)) {
            size_t packet_length = bridge_tx_next(&cursor, length, CDC_DATA_SZ);
            if(packet_length == 0) break;

            furi_hal_cdc_send(BRIDGE_VCP_CH, buffer + cursor.offset, (uint16_t)packet_length);
            FuriStatus status = furi_semaphore_acquire(app->tx_complete, BRIDGE_CDC_TX_TIMEOUT_MS);
            if(app_should_stop(app)) break;
            if(status != FuriStatusOk) {
                (void)bridge_tx_complete(&cursor, false);
                app_request_stop(app, BridgeStopTxTimeout);
                FURI_LOG_E("UUART", "CDC TX completion timed out; stopping bridge");
                break;
            }

            (void)bridge_tx_complete(&cursor, true);
        }
    }

    return 0;
}

static void draw_callback(Canvas* canvas, void* context) {
    UsbUartApp* app = context;
    BridgeLifecycle lifecycle;
    uint32_t usb_rx_dropped;
    uint32_t uart_rx_dropped;
    uint32_t uart_overrun_events;

    FURI_CRITICAL_ENTER();
    lifecycle = app->lifecycle;
    usb_rx_dropped = app->usb_rx_dropped;
    uart_rx_dropped = app->uart_rx_dropped;
    uart_overrun_events = app->uart_overrun_events;
    FURI_CRITICAL_EXIT();

    canvas_clear(canvas);
    canvas_set_font(canvas, FontPrimary);
    canvas_draw_str_aligned(canvas, 64, 9, AlignCenter, AlignCenter, "USB-UART Bridge");
    canvas_set_font(canvas, FontSecondary);
    canvas_draw_str_aligned(canvas, 64, 20, AlignCenter, AlignCenter, "USART 115200 8N1");
    if(lifecycle.armed) {
        canvas_draw_str_aligned(canvas, 64, 30, AlignCenter, AlignCenter, "DTR close armed");
    } else if(lifecycle.connected) {
        canvas_draw_str_aligned(
            canvas, 64, 30, AlignCenter, AlignCenter, "Waiting for stable DTR");
    } else {
        canvas_draw_str_aligned(canvas, 64, 30, AlignCenter, AlignCenter, "Waiting for USB host");
    }

    char status[32];
    snprintf(status, sizeof(status), "USB RX lost: %lu", (unsigned long)usb_rx_dropped);
    canvas_draw_str_aligned(canvas, 64, 41, AlignCenter, AlignCenter, status);
    snprintf(status, sizeof(status), "UART RX lost: %lu", (unsigned long)uart_rx_dropped);
    canvas_draw_str_aligned(canvas, 64, 51, AlignCenter, AlignCenter, status);
    snprintf(status, sizeof(status), "UART overrun: %lu", (unsigned long)uart_overrun_events);
    canvas_draw_str_aligned(canvas, 64, 61, AlignCenter, AlignCenter, status);
}

static void input_callback(InputEvent* input_event, void* context) {
    FuriMessageQueue* queue = context;
    (void)furi_message_queue_put(queue, input_event, 0);
}

static void
    bridge_cleanup(UsbUartApp* app, Gui* gui, ViewPort* view_port, FuriMessageQueue* event_queue) {
    app_request_stop(app, BridgeStopBack);

    if(app->callbacks_installed) {
        furi_hal_cdc_set_callbacks(BRIDGE_VCP_CH, NULL, NULL);
        app->callbacks_installed = false;
    }

    if(app->tx_complete != NULL) (void)furi_semaphore_release(app->tx_complete);
    if(app->usb_to_uart_thread != NULL) {
        if(app->usb_to_uart_started) furi_thread_join(app->usb_to_uart_thread);
        furi_thread_free(app->usb_to_uart_thread);
        app->usb_to_uart_thread = NULL;
    }
    if(app->uart_to_usb_thread != NULL) {
        if(app->uart_to_usb_started) furi_thread_join(app->uart_to_usb_thread);
        furi_thread_free(app->uart_to_usb_thread);
        app->uart_to_usb_thread = NULL;
    }

    if(app->serial_dma_started) furi_hal_serial_dma_rx_stop(app->serial_handle);
    if(app->serial_initialized) furi_hal_serial_deinit(app->serial_handle);
    if(app->serial_handle != NULL) {
        furi_hal_serial_control_release(app->serial_handle);
        app->serial_handle = NULL;
    }
    if(app->expansion_disabled) {
        expansion_enable(app->expansion);
        app->expansion_disabled = false;
    }

    if(app->cli_disabled) {
        furi_hal_usb_unlock();
        if(!furi_hal_usb_set_config(&usb_cdc_single, NULL)) {
            FURI_LOG_E("UUART", "Failed to restore single CDC USB configuration");
        }
        cli_vcp_enable(app->cli_vcp);
        furi_record_close(RECORD_CLI_VCP);
        app->cli_disabled = false;
    }

    if(app->expansion != NULL) furi_record_close(RECORD_EXPANSION);
    if(gui != NULL && view_port != NULL) gui_remove_view_port(gui, view_port);
    if(gui != NULL) furi_record_close(RECORD_GUI);
    if(view_port != NULL) view_port_free(view_port);
    if(event_queue != NULL) furi_message_queue_free(event_queue);
    if(app->usb_rx_stream != NULL) furi_stream_buffer_free(app->usb_rx_stream);
    if(app->uart_rx_stream != NULL) furi_stream_buffer_free(app->uart_rx_stream);
    if(app->tx_complete != NULL) furi_semaphore_free(app->tx_complete);
}

int32_t usb_uart_bridge_app(void* p) {
    UNUSED(p);
    UsbUartApp app = {0};
    bridge_lifecycle_init(&app.lifecycle);

    FuriMessageQueue* event_queue = NULL;
    ViewPort* view_port = NULL;
    Gui* gui = NULL;

    app.usb_rx_stream = furi_stream_buffer_alloc(BRIDGE_STREAM_SIZE, 1);
    app.uart_rx_stream = furi_stream_buffer_alloc(BRIDGE_STREAM_SIZE, 1);
    app.tx_complete = furi_semaphore_alloc(1, 0);
    event_queue = furi_message_queue_alloc(8, sizeof(InputEvent));
    if(app.usb_rx_stream == NULL || app.uart_rx_stream == NULL || app.tx_complete == NULL ||
       event_queue == NULL) {
        FURI_LOG_E("UUART", "Failed to allocate bridge buffers");
        goto cleanup;
    }

    view_port = view_port_alloc();
    if(view_port == NULL) {
        FURI_LOG_E("UUART", "Failed to allocate bridge view");
        goto cleanup;
    }
    view_port_draw_callback_set(view_port, draw_callback, &app);
    view_port_input_callback_set(view_port, input_callback, event_queue);

    gui = furi_record_open(RECORD_GUI);
    gui_add_view_port(gui, view_port, GuiLayerFullscreen);

    app.expansion = furi_record_open(RECORD_EXPANSION);
    expansion_disable(app.expansion);
    app.expansion_disabled = true;

    app.serial_handle = furi_hal_serial_control_acquire(BRIDGE_UART);
    if(app.serial_handle == NULL) {
        FURI_LOG_E("UUART", "Failed to acquire UART");
        goto cleanup;
    }
    furi_hal_serial_init(app.serial_handle, BRIDGE_BAUD);
    app.serial_initialized = true;
    // Error IRQ reporting is required for the overrun counter shown in the UI.
    furi_hal_serial_dma_rx_start(app.serial_handle, app_on_serial_rx_dma, &app, true);
    app.serial_dma_started = true;

    app.cli_vcp = furi_record_open(RECORD_CLI_VCP);
    furi_hal_usb_unlock();
    cli_vcp_disable(app.cli_vcp);
    app.cli_disabled = true;
    if(!furi_hal_usb_set_config(&usb_cdc_single, NULL)) {
        FURI_LOG_E("UUART", "Failed to configure USB CDC");
        goto cleanup;
    }

    CdcCallbacks cdc_callbacks = {
        .tx_ep_callback = app_on_cdc_tx_complete,
        .rx_ep_callback = app_on_cdc_rx,
        .state_callback = app_on_cdc_state,
        .ctrl_line_callback = app_on_cdc_ctrl_line,
        .config_callback = app_on_cdc_line_config,
    };
    furi_hal_cdc_set_callbacks(BRIDGE_VCP_CH, &cdc_callbacks, &app);
    app.callbacks_installed = true;

    app.usb_to_uart_thread =
        furi_thread_alloc_ex("UsbToUart", BRIDGE_WORKER_STACK, usb_to_uart_worker, &app);
    app.uart_to_usb_thread =
        furi_thread_alloc_ex("UartToUsb", BRIDGE_WORKER_STACK, uart_to_usb_worker, &app);
    if(app.usb_to_uart_thread == NULL || app.uart_to_usb_thread == NULL) {
        FURI_LOG_E("UUART", "Failed to allocate bridge workers");
        goto cleanup;
    }

    furi_thread_start(app.usb_to_uart_thread);
    app.usb_to_uart_started = true;
    furi_thread_start(app.uart_to_usb_thread);
    app.uart_to_usb_started = true;

    InputEvent event;
    while(!app_should_stop(&app)) {
        if(furi_message_queue_get(event_queue, &event, BRIDGE_STOP_POLL_MS) == FuriStatusOk &&
           event.key == InputKeyBack && event.type == InputTypeShort) {
            app_request_stop(&app, BridgeStopBack);
        }

        FURI_CRITICAL_ENTER();
        bridge_lifecycle_tick(&app.lifecycle, furi_get_tick());
        FURI_CRITICAL_EXIT();
        view_port_update(view_port);
    }

cleanup:
    bridge_cleanup(&app, gui, view_port, event_queue);
    return 0;
}
