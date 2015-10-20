// +build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runc/libcontainer/configs"
	"github.com/opencontainers/specs"
)

var restoreCommand = cli.Command{
	Name:  "restore",
	Usage: "restore a container from a previous checkpoint",
	Flags: []cli.Flag{
		cli.StringFlag{Name: "image-path", Value: "", Usage: "path to criu image files for restoring"},
		cli.StringFlag{Name: "work-path", Value: "", Usage: "path for saving work files and logs"},
		cli.BoolFlag{Name: "tcp-established", Usage: "allow open tcp connections"},
		cli.BoolFlag{Name: "ext-unix-sk", Usage: "allow external unix sockets"},
		cli.BoolFlag{Name: "shell-job", Usage: "allow shell jobs"},
		cli.BoolFlag{Name: "file-locks", Usage: "handle file locks, for safety"},
	},
	Action: func(context *cli.Context) {
		imagePath := context.String("image-path")
		if imagePath == "" {
			imagePath = getDefaultImagePath(context)
		}
		spec, err := loadSpec(context.Args().First())
		if err != nil {
			fatal(err)
		}
		config, err := createLibcontainerConfig(context.GlobalString("id"), spec)
		if err != nil {
			fatal(err)
		}
		status, err := restoreContainer(context, spec, config, imagePath)
		if err != nil {
			fatal(err)
		}
		os.Exit(status)
	},
}

func restoreContainer(context *cli.Context, spec *specs.LinuxSpec, config *configs.Config, imagePath string) (code int, err error) {
	rootuid := 0
	factory, err := loadFactory(context)
	if err != nil {
		return -1, err
	}
	container, err := factory.Load(context.GlobalString("id"))
	if err != nil {
		container, err = factory.Create(context.GlobalString("id"), config)
		if err != nil {
			return -1, err
		}
	}
	options := criuOptions(context)
	status, err := container.Status()
	if err != nil {
		logrus.Error(err)
	}
	if status == libcontainer.Running {
		fatal(fmt.Errorf("Container with id %s already running", context.GlobalString("id")))
	}
	// ensure that the container is always removed if we were the process
	// that created it.
	defer func() {
		if err != nil {
			return
		}
		status, err := container.Status()
		if err != nil {
			logrus.Error(err)
		}
		if status != libcontainer.Checkpointed {
			if err := container.Destroy(); err != nil {
				logrus.Error(err)
			}
			if err := os.RemoveAll(options.ImagesDirectory); err != nil {
				logrus.Error(err)
			}
		}
	}()
	process := &libcontainer.Process{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	tty, err := newTty(spec.Process.Terminal, process, rootuid)
	if err != nil {
		return -1, err
	}
	handler := newSignalHandler(tty)
	defer handler.Close()
	if err := container.Restore(process, options); err != nil {
		cstatus, cerr := container.Status()
		if cerr != nil {
			logrus.Error(cerr)
		}
		if cstatus == libcontainer.Destroyed {
			dest := filepath.Join(context.GlobalString("root"), context.GlobalString("id"))
			if errVal := os.RemoveAll(dest); errVal != nil {
				logrus.Error(errVal)
			}
		}
		return -1, err
	}
	return handler.forward(process)
}

func criuOptions(context *cli.Context) *libcontainer.CriuOpts {
	imagePath := getCheckpointImagePath(context)
	if err := os.MkdirAll(imagePath, 0655); err != nil {
		fatal(err)
	}
	return &libcontainer.CriuOpts{
		ImagesDirectory:         imagePath,
		WorkDirectory:           context.String("work-path"),
		LeaveRunning:            context.Bool("leave-running"),
		TcpEstablished:          context.Bool("tcp-established"),
		ExternalUnixConnections: context.Bool("ext-unix-sk"),
		ShellJob:                context.Bool("shell-job"),
		FileLocks:               context.Bool("file-locks"),
	}
}
