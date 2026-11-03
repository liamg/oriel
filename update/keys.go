package update

import "crypto/ed25519"

// releaseKeys is the release public keys this build trusts, hex and comma-separated, set when CI links a release:
// -ldflags "-X github.com/liamg/oriel/update.releaseKeys=…". Two during a rotation: the old and the new.
var releaseKeys string

// Keys is the release keys this build trusts; none in a build CI didn't stamp, which then refuses every update.
func Keys() []ed25519.PublicKey {
	keys, err := ReleaseKeys(releaseKeys)
	if err != nil {
		return nil
	}
	return keys
}
