package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

type RunFile struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

func NewRunFile() (*RunFile, error) {
	info := &RunFile{}
	if _, err := os.Stat(info.getRunFilePath()); err == nil {
		err = info.Load()
		if err != nil {
			return nil, err
		}

		if info.IsProcessRunning() {
			fmt.Println("Another instance is already running. Exiting...")
			os.Exit(0)
		}
	}
	return info, nil
}

func (info *RunFile) getRunFilePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(configDir, ".spore-drive.run")
}

func (info *RunFile) Save(listener net.Listener) error {
	info.Port = listener.Addr().(*net.TCPAddr).Port
	info.PID = os.Getpid()
	runFile, err := os.Create(info.getRunFilePath())
	if err != nil {
		return err
	}
	defer runFile.Close()

	runEncoder := json.NewEncoder(runFile)
	return runEncoder.Encode(info)
}

func (info *RunFile) Remove() {
	os.Remove(info.getRunFilePath())
}

func (info *RunFile) Load() error {
	runFile, err := os.Open(info.getRunFilePath())
	if err != nil {
		return err
	}
	defer runFile.Close()

	runDecoder := json.NewDecoder(runFile)
	return runDecoder.Decode(info)
}

func (info *RunFile) IsProcessRunning() bool {
	if process, err := os.FindProcess(info.PID); err == nil {
		if err := process.Signal(syscall.Signal(0)); err == nil {
			return true
		}
	}
	return false
}
