package amnezia

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/sagernet/sing-box/transport/wireguard"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/sagernet/wireguard-go/conn"
	"github.com/sagernet/wireguard-go/device"
	"github.com/sagernet/wireguard-go/ipc"
)

func start(e *Endpoint, resolve bool) error {
	if common.Any(e.peers, func(peer peerConfig) bool {
		return !peer.endpoint.IsValid() && peer.destination.IsFqdn()
	}) {
		if !resolve {
			return nil
		}
		for peerIndex, peer := range e.peers {
			if peer.endpoint.IsValid() || !peer.destination.IsFqdn() {
				continue
			}
			destinationAddress, err := e.options.ResolvePeer(peer.destination.Fqdn)
			if err != nil {
				return E.Cause(err, "resolve endpoint domain for peer[", peerIndex, "]: ", peer.destination)
			}
			e.peers[peerIndex].endpoint = netip.AddrPortFrom(destinationAddress, peer.destination.Port)
		}
	} else if resolve {
		return nil
	}

	var bind conn.Bind
	wgListener, isWgListener := e.options.Dialer.(conn.Listener)
	if isWgListener {
		bind = conn.NewStdNetBind(wgListener)
	} else {
		var (
			isConnect   bool
			connectAddr netip.AddrPort
			reserved    [3]uint8
		)
		if len(e.peers) == 1 {
			isConnect = true
			connectAddr = e.peers[0].endpoint
			reserved = e.peers[0].reserved
		}
		bind = wireguard.NewClientBind(e.options.Context, e.options.Logger, e.options.Dialer, isConnect, connectAddr, reserved)
	}
	if isWgListener || len(e.peers) > 1 {
		for _, peer := range e.peers {
			if peer.reserved != [3]uint8{} {
				bind.SetReservedForEndpoint(peer.endpoint, peer.reserved)
			}
		}
	}
	err := e.tunDevice.Start()
	if err != nil {
		return err
	}
	logger := &device.Logger{
		Verbosef: func(format string, args ...interface{}) {
			e.options.Logger.Debug(fmt.Sprintf(strings.ToLower(format), args...))
		},
		Errorf: func(format string, args ...interface{}) {
			e.options.Logger.Error(fmt.Sprintf(strings.ToLower(format), args...))
		},
	}
	wgDevice := device.NewDevice(e.options.Context, e.tunDevice, bind, logger, e.options.Workers)

	uapi, err := ipc.UAPIListen(e.options.Name)
	if err != nil {
		return fmt.Errorf("failed to listen on uapi socket: %v", err)
	}

	go func() {
		for {
			select {
			case <-e.options.Context.Done():
				uapi.Close()
				return
			default:
				conn, err := uapi.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					e.options.Logger.Error(E.Cause(err, "uapi accept error"))
					continue // any other accept error, just continue
				}
				go wgDevice.IpcHandle(conn)
			}
		}
	}()

	e.tunDevice.SetDevice(wgDevice)
	ipcConf := e.ipcConf
	for _, peer := range e.peers {
		ipcConf += peer.GenerateIpcLines()
	}
	err = wgDevice.IpcSet(ipcConf)
	if err != nil {
		return E.Cause(err, "setup wireguard: \n", ipcConf)
	}
	e.device = wgDevice
	e.pauseManager = service.FromContext[pause.Manager](e.options.Context)
	if e.pauseManager != nil {
		e.pauseCallback = e.pauseManager.RegisterCallback(e.onPauseUpdated)
	}
	return nil
}
