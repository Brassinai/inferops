package events

import (
	"fmt"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Recorder records Kubernetes Events for InferOps resources.
type Recorder interface {
	// Event records a normal event.
	Event(object runtime.Object, eventType, reason, message string)
	// Eventf records a formatted normal event.
	Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{})
	// Warning records a warning event.
	Warning(object runtime.Object, reason, message string)
	// Warningf records a formatted warning event.
	Warningf(object runtime.Object, reason, messageFmt string, args ...interface{})
}

// Success reason constants used in Events.  Failure reasons live in the
// v1alpha1 API package alongside the condition types so that conditions and
// Events share the same stable reason codes.
const (
	ReasonRuntimeResolved  = v1alpha1.ReasonRuntimeResolved
	ReasonSpecValidated    = v1alpha1.ReasonSpecValidated
	ReasonSecretsAvailable = v1alpha1.ReasonSecretsAvailable
)

// NoOpRecorder discards all events.  It is useful in tests and in binaries that
// have not yet wired a real Kubernetes event recorder.
type NoOpRecorder struct{}

func (NoOpRecorder) Event(_ runtime.Object, _, _, _ string)                    {}
func (NoOpRecorder) Eventf(_ runtime.Object, _, _, _ string, _ ...interface{}) {}
func (NoOpRecorder) Warning(_ runtime.Object, _, _ string)                     {}
func (NoOpRecorder) Warningf(_ runtime.Object, _, _ string, _ ...interface{})  {}

// FakeRecorder records events in memory for tests.
type FakeRecorder struct {
	Events []RecordedEvent
}

// RecordedEvent captures a single recorded event.
type RecordedEvent struct {
	EventType string
	Reason    string
	Message   string
}

func (f *FakeRecorder) Event(object runtime.Object, eventType, reason, message string) {
	f.Events = append(f.Events, RecordedEvent{EventType: eventType, Reason: reason, Message: message})
}

func (f *FakeRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	f.Event(object, eventType, reason, fmt.Sprintf(messageFmt, args...))
}

func (f *FakeRecorder) Warning(object runtime.Object, reason, message string) {
	f.Event(object, "Warning", reason, message)
}

func (f *FakeRecorder) Warningf(object runtime.Object, reason, messageFmt string, args ...interface{}) {
	f.Warning(object, reason, fmt.Sprintf(messageFmt, args...))
}
