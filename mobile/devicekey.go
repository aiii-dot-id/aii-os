package mobile

import "errors"

type DeviceKey interface {
	Wrap(plain []byte) ([]byte, error)
	Unwrap(sealed []byte) ([]byte, error)
	Holder() string
}

var errNoDeviceKey = errors.New("mobile: the shell gave no keystore, and on a phone the identity's key is kept only wrapped by the phone's keystore, so nothing was started")
