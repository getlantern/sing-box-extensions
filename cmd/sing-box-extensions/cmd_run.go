package main

import (
	"context"
	"fmt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/libbox"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	"os"
	"os/signal"
	"path/filepath"
	runtimeDebug "runtime/debug"
	"syscall"
	"time"
)

type RunCmd struct {
}

func prepare(configFile string) (*libbox.BoxService, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(newBaseContext())

	var config string

	if data, err := os.ReadFile(configFile); err != nil {
		return nil, cancel, fmt.Errorf("reading config file: %w", err)
	} else {
		config = string(data)
	}
	if err := libbox.Setup(&libbox.SetupOptions{
		BasePath:    args.DataDir,
		WorkingPath: filepath.Join(args.DataDir, "data"),
		TempPath:    filepath.Join(args.DataDir, "temp"),
	}); err != nil {
		return nil, cancel, fmt.Errorf("setup libbox: %w", err)
	}

	instance, err := libbox.NewServiceWithContext(ctx, config, nil)
	return instance, cancel, err
}

func create(config string) (*libbox.BoxService, context.CancelFunc, error) {
	instance, cancel, err := prepare(config)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("setup libbox: %w", err)
	}

	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer func() {
		signal.Stop(osSignals)
		close(osSignals)
	}()
	startCtx, finishStart := context.WithCancel(context.Background())
	go func() {
		_, loaded := <-osSignals
		if loaded {
			cancel()
			closeMonitor(startCtx)
		}
	}()
	err = instance.Start()
	finishStart()
	if err != nil {
		cancel()
		return nil, nil, E.Cause(err, "start service")
	}
	return instance, cancel, nil
}

func closeMonitor(ctx context.Context) {
	time.Sleep(C.FatalStopTimeout)
	select {
	case <-ctx.Done():
		return
	default:
	}
	log.Fatal("sing-box did not close!")
}

func (c *RunCmd) Run() error {
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(osSignals)
	for {
		instance, cancel, err := create(args.ConfigFile)
		if err != nil {
			return err
		}
		runtimeDebug.FreeOSMemory()
		for {
			osSignal := <-osSignals
			if osSignal == syscall.SIGHUP {
				err = check(args.ConfigFile)
				if err != nil {
					log.Error(E.Cause(err, "reload service"))
					continue
				}
			}
			cancel()
			closeCtx, closed := context.WithCancel(context.Background())
			go closeMonitor(closeCtx)
			err = instance.Close()
			closed()
			if osSignal != syscall.SIGHUP {
				if err != nil {
					log.Error(E.Cause(err, "sing-box did not closed properly"))
				}
				return nil
			}
			break
		}
	}
}
