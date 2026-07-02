package events

import (
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
)

func TestNoOpRecorder(t *testing.T) {
	t.Parallel()

	var recorder Recorder = NoOpRecorder{}
	deployment := &v1alpha1.ModelDeployment{}

	// Should not panic or record anything.
	recorder.Event(deployment, "Normal", "Reason", "message")
	recorder.Eventf(deployment, "Normal", "Reason", "message %s", "formatted")
	recorder.Warning(deployment, "Reason", "message")
	recorder.Warningf(deployment, "Reason", "message %s", "formatted")
}

func TestFakeRecorderCapturesEvents(t *testing.T) {
	t.Parallel()

	recorder := &FakeRecorder{}
	deployment := &v1alpha1.ModelDeployment{}

	recorder.Event(deployment, "Normal", ReasonRuntimeResolved, "runtime resolved")
	recorder.Warningf(deployment, "SpecInvalid", "spec invalid: %s", "missing ref")

	if len(recorder.Events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(recorder.Events))
	}

	if got, want := recorder.Events[0].EventType, "Normal"; got != want {
		t.Errorf("event[0].type = %q, want %q", got, want)
	}
	if got, want := recorder.Events[0].Reason, ReasonRuntimeResolved; got != want {
		t.Errorf("event[0].reason = %q, want %q", got, want)
	}

	if got, want := recorder.Events[1].EventType, "Warning"; got != want {
		t.Errorf("event[1].type = %q, want %q", got, want)
	}
	if got, want := recorder.Events[1].Message, "spec invalid: missing ref"; got != want {
		t.Errorf("event[1].message = %q, want %q", got, want)
	}
}
