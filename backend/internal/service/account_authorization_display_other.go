//go:build !linux

package service

import "context"

// Native non-Linux browser environments retain their existing display setup.
func authorizationBrowserDisplayEnv(ctx context.Context, _ string) ([]string, func(), error) {
	return nil, func() {}, ctx.Err()
}
