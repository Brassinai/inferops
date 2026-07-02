package status

import (
	"testing"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetConditionAppendsNewCondition(t *testing.T) {
	t.Parallel()

	conditions := []v1alpha1.Condition{}
	SetCondition(&conditions, 3, v1alpha1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "AllGood",
	})

	if len(conditions) != 1 {
		t.Fatalf("len(conditions) = %d, want 1", len(conditions))
	}
	if conditions[0].Type != "Ready" {
		t.Errorf("condition type = %q, want Ready", conditions[0].Type)
	}
	if conditions[0].ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want 3", conditions[0].ObservedGeneration)
	}
	if conditions[0].LastTransitionTime.IsZero() {
		t.Error("LastTransitionTime was not set")
	}
}

func TestSetConditionUpdatesExistingCondition(t *testing.T) {
	t.Parallel()

	conditions := []v1alpha1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Reason: "OldReason", Message: "old"},
	}
	SetCondition(&conditions, 2, v1alpha1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "NewReason",
		Message: "new",
	})

	if len(conditions) != 1 {
		t.Fatalf("len(conditions) = %d, want 1", len(conditions))
	}
	if conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("status = %q, want True", conditions[0].Status)
	}
	if conditions[0].Reason != "NewReason" {
		t.Errorf("reason = %q, want NewReason", conditions[0].Reason)
	}
}

func TestSetConditionPreservesTimestampWhenUnchanged(t *testing.T) {
	t.Parallel()

	original := metav1.Now()
	conditions := []v1alpha1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Same", Message: "same", LastTransitionTime: original, ObservedGeneration: 1},
	}
	changed := SetCondition(&conditions, 2, v1alpha1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Same",
		Message: "same",
	})

	if !changed {
		t.Error("SetCondition() reported no change when observedGeneration changed")
	}
	if len(conditions) != 1 {
		t.Fatalf("len(conditions) = %d, want 1", len(conditions))
	}
	if !conditions[0].LastTransitionTime.Equal(&original) {
		t.Error("LastTransitionTime changed even though condition was unchanged")
	}
	if conditions[0].ObservedGeneration != 2 {
		t.Errorf("ObservedGeneration = %d, want 2", conditions[0].ObservedGeneration)
	}
}

func TestSetConditionPreservesTimestampWhenReasonChanges(t *testing.T) {
	t.Parallel()

	original := metav1.Now()
	conditions := []v1alpha1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Old", LastTransitionTime: original},
	}

	SetCondition(&conditions, 1, v1alpha1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionFalse,
		Reason: "New",
	})

	if !conditions[0].LastTransitionTime.Equal(&original) {
		t.Error("LastTransitionTime changed without a status transition")
	}
}

func TestSetConditionReportsNoChange(t *testing.T) {
	t.Parallel()

	original := metav1.Now()
	conditions := []v1alpha1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Same",
			Message:            "same",
			ObservedGeneration: 1,
			LastTransitionTime: original,
		},
	}

	changed := SetCondition(&conditions, 1, v1alpha1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Same",
		Message: "same",
	})
	if changed {
		t.Error("SetCondition() reported a change for an identical condition")
	}
}

func TestFindCondition(t *testing.T) {
	t.Parallel()

	conditions := []v1alpha1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
		{Type: "RuntimeResolved", Status: metav1.ConditionFalse},
	}

	cond, found := FindCondition(conditions, "RuntimeResolved")
	if !found {
		t.Fatal("expected to find RuntimeResolved condition")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("status = %q, want False", cond.Status)
	}

	_, found = FindCondition(conditions, "Missing")
	if found {
		t.Error("expected not to find Missing condition")
	}
}

func TestRemoveCondition(t *testing.T) {
	t.Parallel()

	conditions := []v1alpha1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
		{Type: "RuntimeResolved", Status: metav1.ConditionFalse},
	}

	RemoveCondition(&conditions, "Ready")
	if len(conditions) != 1 {
		t.Fatalf("len(conditions) = %d, want 1", len(conditions))
	}
	if conditions[0].Type != "RuntimeResolved" {
		t.Errorf("remaining condition = %q, want RuntimeResolved", conditions[0].Type)
	}
}
