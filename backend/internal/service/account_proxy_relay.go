package service

import "context"

type accountProxyRelayContextKey struct{}

// WithAccountProxyRelay marks the trusted Anthropic relay protocol: the selected
// egress is passed in its proxy query parameter, while the relay itself is reached
// directly. The fallback preserves explicitly selected administrative requests.
func WithAccountProxyRelay(ctx context.Context, fallback string) context.Context {
	return context.WithValue(ctx, accountProxyRelayContextKey{}, fallback)
}

func AccountProxyRelayFromContext(ctx context.Context) (string, bool) {
	fallback, ok := ctx.Value(accountProxyRelayContextKey{}).(string)
	return fallback, ok
}
