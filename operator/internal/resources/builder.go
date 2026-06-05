package resources

// Builder will create Kubernetes resources owned by model custom resources.
type Builder struct{}

// NewBuilder creates a resource builder.
func NewBuilder() Builder {
	return Builder{}
}
