package status

import (
	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetCondition updates or appends a condition in the condition list. It
// preserves LastTransitionTime unless Status changes and always refreshes
// ObservedGeneration. The return value reports whether the list changed.
func SetCondition(conditions *[]v1alpha1.Condition, observedGeneration int64, condition v1alpha1.Condition) bool {
	if conditions == nil {
		return false
	}
	condition.ObservedGeneration = observedGeneration

	for i := range *conditions {
		if (*conditions)[i].Type == condition.Type {
			if (*conditions)[i].Status == condition.Status {
				condition.LastTransitionTime = (*conditions)[i].LastTransitionTime
			} else if condition.LastTransitionTime.IsZero() {
				condition.LastTransitionTime = metav1.Now()
			}
			if conditionsEqual((*conditions)[i], condition) {
				return false
			}
			(*conditions)[i] = condition
			return true
		}
	}

	if condition.LastTransitionTime.IsZero() {
		condition.LastTransitionTime = metav1.Now()
	}
	*conditions = append(*conditions, condition)
	return true
}

// FindCondition returns the condition with the requested type and true if found.
func FindCondition(conditions []v1alpha1.Condition, conditionType string) (v1alpha1.Condition, bool) {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return conditions[i], true
		}
	}
	return v1alpha1.Condition{}, false
}

// RemoveCondition removes a condition of the requested type.
func RemoveCondition(conditions *[]v1alpha1.Condition, conditionType string) {
	if conditions == nil {
		return
	}
	result := make([]v1alpha1.Condition, 0, len(*conditions))
	for _, c := range *conditions {
		if c.Type != conditionType {
			result = append(result, c)
		}
	}
	*conditions = result
}

func conditionsEqual(a, b v1alpha1.Condition) bool {
	return a.Type == b.Type &&
		a.Status == b.Status &&
		a.ObservedGeneration == b.ObservedGeneration &&
		a.LastTransitionTime.Equal(&b.LastTransitionTime) &&
		a.Reason == b.Reason &&
		a.Message == b.Message
}
