//go:build !ghostty_native

package ghostty

import "errors"

const nativeBuilt = false

func newNative(int, int, int) (machine, error) {
	return nil, errors.New("the native core isn't built in: build with -tags ghostty_native after native/build.sh")
}
