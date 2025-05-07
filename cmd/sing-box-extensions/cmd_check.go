package main

import "fmt"

type CheckCmd struct {
}

func check(configFile string) error {
	instance, cancel, err := prepare(configFile)

	if err == nil {
		_ = instance.Close()
	}
	cancel()
	return err
}

func (c *CheckCmd) Run() error {
	err := check(args.ConfigFile)
	if err == nil {
		fmt.Println("Configuration is valid")
	} else {
		return err
	}
	return nil
}
