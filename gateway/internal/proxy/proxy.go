package proxy

// Proxy will forward OpenAI-compatible requests to nano-vLLM runtime pods.
type Proxy struct{}

// New creates a proxy.
func New() Proxy {
	return Proxy{}
}
