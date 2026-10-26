package marauder

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const bridgeExitDTRArmDelay = 500 * time.Millisecond

type bridgeExitDTRPort interface {
	SetDTR(high bool) error
}

// EnableBridgeExitDTR arms the custom-FAP bridge exit signal. The DTR line
// stays high after the cancellable arm delay and is lowered by Close.
func (m *Marauder) EnableBridgeExitDTR(ctx context.Context) error {
	if m == nil {
		return errors.New("marauder client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closed {
		return errors.New("marauder port is closed")
	}
	if m.bridgeExitDTR {
		return nil
	}
	p, ok := m.port.(bridgeExitDTRPort)
	if !ok {
		return errors.New("marauder serial port does not support DTR")
	}
	// Mark the line as owned before asserting it so Close lowers it even
	// when SetDTR reports an error after partially changing the device state.
	m.bridgeExitDTR = true
	if err := p.SetDTR(true); err != nil {
		return fmt.Errorf("asserting bridge-exit DTR: %w", err)
	}

	timer := time.NewTimer(bridgeExitDTRArmDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for bridge-exit DTR arm delay: %w", ctx.Err())
	}
}
