package scheduler

// Planner will contain GPU and placement-aware scheduling helpers.
type Planner struct{}

// NewPlanner creates a scheduling planner.
func NewPlanner() Planner {
	return Planner{}
}
