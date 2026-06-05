package metrics

// Recorder will expose gateway metrics.
type Recorder struct{}

// NewRecorder creates a metrics recorder.
func NewRecorder() Recorder {
	return Recorder{}
}
