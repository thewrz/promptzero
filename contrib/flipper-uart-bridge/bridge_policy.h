#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define BRIDGE_CDC_PACKET_MAX   64u
#define BRIDGE_DTR_ARM_DWELL_MS 500u

typedef struct {
    size_t offset;
    size_t pending_length;
    bool awaiting_completion;
    bool failed;
} BridgeTxCursor;

typedef enum {
    BridgeStopNone,
    BridgeStopBack,
    BridgeStopDisconnect,
    BridgeStopDtr,
    BridgeStopTxTimeout,
} BridgeStopReason;

typedef struct {
    bool connected;
    bool dtr_high;
    bool dtr_close_ready;
    bool high_since_valid;
    bool armed;
    uint32_t high_since;
    BridgeStopReason stop_reason;
} BridgeLifecycle;

size_t bridge_tx_next(BridgeTxCursor* cursor, size_t total, size_t packet_limit);
bool bridge_tx_complete(BridgeTxCursor* cursor, bool completed);
uint32_t bridge_loss_record(uint32_t current, size_t offered, size_t accepted);
size_t bridge_uart_rx_drain_length(bool data_event, bool idle_event, size_t data_len);

void bridge_lifecycle_init(BridgeLifecycle* lifecycle);
void bridge_lifecycle_set_connected(BridgeLifecycle* lifecycle, bool connected, uint32_t now);
void bridge_lifecycle_set_dtr(BridgeLifecycle* lifecycle, bool high, uint32_t now);
void bridge_lifecycle_tick(BridgeLifecycle* lifecycle, uint32_t now);
void bridge_lifecycle_request_stop(BridgeLifecycle* lifecycle, BridgeStopReason reason);
bool bridge_lifecycle_should_stop(const BridgeLifecycle* lifecycle);
