//go:build !with_gvisor

package amnezia

import "github.com/sagernet/sing-tun"

func newStackDevice(options DeviceOptions) (Device, error) {
	return nil, tun.ErrGVisorNotIncluded
}

func newSystemStackDevice(options DeviceOptions) (Device, error) {
	return nil, tun.ErrGVisorNotIncluded
}
