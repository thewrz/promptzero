package mock

// WriteChunkHandler receives a completed storage write_chunk payload. The
// bool controls whether the mock emits the firmware prompt after the response.
type WriteChunkHandler func(path string, data []byte) (response string, prompt bool)

// WithWriteChunkHandler installs a handler for storage write_chunk payloads.
// Returning prompt=false simulates a firmware write that never completes.
func WithWriteChunkHandler(handler WriteChunkHandler) Option {
	return func(m *Mock) { m.writeChunkHandler = handler }
}
