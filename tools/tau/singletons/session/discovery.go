package session

import (
	"fmt"
	"os"
	"path"
	"strconv"

	"github.com/mitchellh/go-ps"
)

func parentId() int {
	var ppid int

	envppid := os.Getenv("TAU_PPID")
	if envppid != "" {
		ppid, _ = strconv.Atoi(envppid)
	} else {
		ppid = os.Getppid()
	}

	process, err := ps.FindProcess(ppid)
	if err != nil {
		return ppid
	}

	// This is for `go run .`
	if process.Executable() == "go" || process.Executable() == "node" {
		return process.PPid()
	}

	return process.Pid()
}

func discoverOrCreateConfigFileLoc() (string, error) {
	grandPid := parentId()

	var err error
	processDir, found := nearestProcessDirectory(grandPid)
	if !found {
		processDir, err = createProcessDirectory(grandPid)
		if err != nil {
			return "", err
		}
	}

	return processDir, nil
}

/*
Nearest process directory will climb up the process tree until it finds a
directory or the pid reaches 1
*/
func nearestProcessDirectory(pid int) (processDir string, found bool) {
	processDir = directoryFromPid(pid)

	_, err := os.Stat(processDir)
	if err != nil {
		process, err := ps.FindProcess(pid)
		if err != nil {
			return
		}
		if process == nil {
			return
		}

		ppid := process.PPid()
		if ppid == 1 {
			return
		}

		processDir = directoryFromPid(ppid)

		_, err = os.Stat(processDir)
		if err != nil {
			return nearestProcessDirectory(ppid)
		}
	}

	return processDir, true
}

func directoryFromPid(pid int) string {
	return path.Join(os.TempDir(), fmt.Sprintf("%s-%d", sessionDirPrefix, pid))
}

func createProcessDirectory(pid int) (string, error) {
	processDir := directoryFromPid(pid)

	err := os.Mkdir(processDir, os.ModePerm)
	if err != nil {
		return "", err
	}

	return processDir, nil
}
