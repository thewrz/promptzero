#include "bridge_policy.h"

#include <limits.h>

size_t bridge_tx_next(BridgeTxCursor* cursor, size_t total, size_t packet_limit) {
    if(cursor->failed || cursor->awaiting_completion || packet_limit == 0 ||
       cursor->offset >= total) {
        return 0;
    }

    size_t remaining = total - cursor->offset;
    cursor->pending_length = remaining < packet_limit ? remaining : packet_limit;
    cursor->awaiting_completion = true;
    return cursor->pending_length;
}

bool bridge_tx_complete(BridgeTxCursor* cursor, bool completed) {
    if(!cursor->awaiting_completion) return false;

    cursor->awaiting_completion = false;
    if(!completed) {
        cursor->failed = true;
        cursor->pending_length = 0;
        return false;
    }

    cursor->offset += cursor->pending_length;
    cursor->pending_length = 0;
    return true;
}

uint32_t bridge_loss_record(uint32_t current, size_t offered, size_t accepted) {
    if(accepted >= offered) return current;

    size_t lost = offered - accepted;
    uint32_t increment = lost > UINT32_MAX ? UINT32_MAX : (uint32_t)lost;
    if(UINT32_MAX - current < increment) return UINT32_MAX;
    return current + increment;
}

size_t bridge_uart_rx_drain_length(bool data_event, bool idle_event, size_t data_len) {
    return (data_event || idle_event) ? data_len : 0;
}

void bridge_lifecycle_init(BridgeLifecycle* lifecycle) {
    *lifecycle = (BridgeLifecycle){0};
}

static void bridge_lifecycle_update_arming(BridgeLifecycle* lifecycle, uint32_t now) {
    if(lifecycle->connected && lifecycle->dtr_high && lifecycle->high_since_valid &&
       (uint32_t)(now - lifecycle->high_since) >= BRIDGE_DTR_ARM_DWELL_MS) {
        lifecycle->armed = true;
    }
}

void bridge_lifecycle_set_connected(BridgeLifecycle* lifecycle, bool connected, uint32_t now) {
    if(!connected) bridge_lifecycle_update_arming(lifecycle, now);
    lifecycle->connected = connected;
    if(!connected) {
        lifecycle->dtr_high = false;
        lifecycle->high_since_valid = false;
        if(lifecycle->armed) bridge_lifecycle_request_stop(lifecycle, BridgeStopDisconnect);
    }
}

void bridge_lifecycle_set_dtr(BridgeLifecycle* lifecycle, bool high, uint32_t now) {
    if(!lifecycle->connected) return;

    if(high) {
        if(!lifecycle->dtr_high) {
            lifecycle->dtr_high = true;
            lifecycle->high_since = now;
            lifecycle->high_since_valid = true;
        }
        return;
    }

    bridge_lifecycle_update_arming(lifecycle, now);
    if(lifecycle->dtr_high && lifecycle->armed) {
        bridge_lifecycle_request_stop(lifecycle, BridgeStopDtr);
    }
    lifecycle->dtr_high = false;
    lifecycle->high_since_valid = false;
}

void bridge_lifecycle_tick(BridgeLifecycle* lifecycle, uint32_t now) {
    bridge_lifecycle_update_arming(lifecycle, now);
}

void bridge_lifecycle_request_stop(BridgeLifecycle* lifecycle, BridgeStopReason reason) {
    if(lifecycle->stop_reason == BridgeStopNone && reason != BridgeStopNone) {
        lifecycle->stop_reason = reason;
    }
}

bool bridge_lifecycle_should_stop(const BridgeLifecycle* lifecycle) {
    return lifecycle->stop_reason != BridgeStopNone;
}
