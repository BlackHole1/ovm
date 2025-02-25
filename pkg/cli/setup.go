// SPDX-FileCopyrightText: 2024 OOMOL, Inc. <https://www.oomol.com>
// SPDX-License-Identifier: MPL-2.0

package cli

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/oomol-lab/ovm/pkg/utils"
	"golang.org/x/sync/errgroup"
)

type Context struct {
	Name            string
	VersionsPath    string
	LogPath         string
	SocketPath      string
	IsCliMode       bool
	LockFile        string
	ExecutablePath  string
	BindPID         int
	EventSocketPath string
	PowerSaveMode   bool
	KernelDebug     bool

	Endpoint          string
	SSHPort           int
	SSHKeyPath        string
	SSHPrivateKeyPath string
	SSHPublicKeyPath  string
	SSHPublicKey      string

	ForwardSocketPath     string
	SocketNetworkPath     string
	SocketInitrdVSockPath string
	SocketReadyPath       string
	RestfulSocketPath     string
	TimeSyncSocketPath    string
	SSHAuthSocketPath     string

	CPUS         uint
	MemoryBytes  uint64
	KernelPath   string
	InitrdPath   string
	RootfsPath   string
	TargetPath   string
	DiskDataPath string
	DiskTmpPath  string
}

func Init() *Context {
	return &Context{}
}

func (c *Context) PreSetup() error {
	g := errgroup.Group{}

	g.Go(c.basic)
	g.Go(c.logPath)

	return g.Wait()
}

func (c *Context) Setup() error {
	g := errgroup.Group{}

	g.Go(c.socketPath)
	g.Go(c.ssh)
	g.Go(c.sshPort)
	g.Go(c.target)

	return g.Wait()
}

func (c *Context) basic() error {
	c.Name = name
	c.CPUS = cpus
	c.MemoryBytes = memory * 1024 * 1024
	c.IsCliMode = cliMode
	c.BindPID = bindPID
	c.EventSocketPath = eventSocketPath
	c.PowerSaveMode = powerSaveMode
	c.KernelDebug = kernelDebug

	if err := os.MkdirAll("/tmp/ovm", 0755); err != nil {
		return err
	}

	if p, err := os.Executable(); err != nil {
		return fmt.Errorf("get executable path error: %w", err)
	} else {
		p, err := filepath.EvalSymlinks(p)
		if err != nil {
			return fmt.Errorf("eval symlink error: %w", err)
		}

		c.ExecutablePath = strings.ToLower(p)

		sum := md5.Sum([]byte(c.ExecutablePath))
		hash := hex.EncodeToString(sum[:])
		c.LockFile = "/tmp/ovm/" + hash + "-" + name + ".pid"
	}

	return nil
}

func (c *Context) socketPath() error {
	p, err := filepath.Abs(socketPath)
	if err != nil {
		return err
	}

	c.SocketPath = p
	c.ForwardSocketPath = path.Join(p, name+"-podman.sock")
	c.SocketNetworkPath = path.Join(p, name+"-vfkit-network.sock")
	c.SocketInitrdVSockPath = path.Join(p, name+"-initrd-vsock.sock")
	c.SocketReadyPath = path.Join(p, name+"-ready.sock")
	c.RestfulSocketPath = path.Join(p, name+"-restful.sock")
	c.TimeSyncSocketPath = path.Join(p, name+"-sync-time.sock")
	c.SSHAuthSocketPath = path.Join(p, name+"-ssh-auth.sock")

	c.Endpoint = "unix://" + c.SocketNetworkPath

	if err := os.RemoveAll(c.SocketPath); err != nil {
		return err
	}

	if err := os.MkdirAll(c.SocketPath, 0755); err != nil {
		return err
	}

	return nil
}

func (c *Context) ssh() error {
	p, err := filepath.Abs(sshKeyPath)
	if err != nil {
		return err
	}

	c.SSHKeyPath = p
	c.SSHPrivateKeyPath = path.Join(p, name)
	c.SSHPublicKeyPath = path.Join(p, name+".pub")

	if err := os.MkdirAll(p, 0700); err != nil {
		return err
	}

	{
		g := errgroup.Group{}
		g.Go(func() error {
			_, err := os.Stat(c.SSHPrivateKeyPath)
			return err
		})
		g.Go(func() error {
			_, err := os.Stat(c.SSHPublicKeyPath)
			return err
		})
		if err := g.Wait(); err != nil {
			_ = os.RemoveAll(c.SSHPrivateKeyPath)
			_ = os.RemoveAll(c.SSHPublicKeyPath)
			if err := utils.GenerateSSHKey(c.SSHKeyPath, name); err != nil {
				return err
			}
		}
	}

	{
		f, err := os.Open(c.SSHPublicKeyPath)
		if err != nil {
			return err
		}
		defer f.Close()

		b, err := io.ReadAll(f)
		if err != nil {
			return err
		}

		c.SSHPublicKey = strings.TrimSpace(string(b))
	}

	return nil
}

func (c *Context) sshPort() error {
	port, err := utils.FindUsablePort(2233)
	if err != nil {
		return err
	}

	c.SSHPort = port

	return nil
}

func (c *Context) logPath() error {
	p, err := filepath.Abs(logPath)
	if err != nil {
		return err
	}

	c.LogPath = p

	return os.MkdirAll(c.LogPath, 0755)
}

func (c *Context) target() error {
	p, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}

	c.TargetPath = p
	if err := os.MkdirAll(c.TargetPath, 0755); err != nil {
		return err
	}

	c.VersionsPath = path.Join(c.TargetPath, "versions.json")
	c.KernelPath = path.Join(c.TargetPath, filepath.Base(kernelPath))
	c.InitrdPath = path.Join(c.TargetPath, filepath.Base(initrdPath))
	c.RootfsPath = path.Join(c.TargetPath, filepath.Base(rootfsPath))
	c.DiskDataPath = path.Join(c.TargetPath, "data.img")
	c.DiskTmpPath = path.Join(c.TargetPath, "tmp.img")

	target, err := newTarget(c.TargetPath, kernelPath, initrdPath, rootfsPath, c.DiskDataPath, c.VersionsPath)
	if err != nil {
		return err
	}

	if err := target.handle(); err != nil {
		return err
	}

	if _, err := os.Stat(c.DiskTmpPath); err != nil {
		if err := utils.CreateSparseFile(c.DiskTmpPath, 1*1024*1024*1024*1024); err != nil {
			return err
		}
	}

	return nil
}
