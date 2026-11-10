//go:build desktop && (windows || darwin)

package main

import "context"

func desktopTrayAvailable(context.Context) (bool, string) {
	return true, ""
}
