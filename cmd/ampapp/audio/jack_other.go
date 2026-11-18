//go:build !linux

package audio

import "errors"

var errJackUnavailable = errors.New("o backend JACK próprio só existe no Linux")

func jackList() (outs, ins []jackDevice, err error)  { return nil, nil, errJackUnavailable }
func jackDeviceInfos(devs []jackDevice) []DeviceInfo { return nil }
func (h *Host) openJack(cfg Config) error            { return errJackUnavailable }
func (h *Host) closeJack()                           {}
func jackBuffer() uint32                             { return 0 }
func jackShutdown() bool                             { return false }
func jackAvailable() bool                            { return false }
