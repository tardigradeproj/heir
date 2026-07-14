package integration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
	"golang.org/x/crypto/ssh"
)

type SSHConn struct {
	client *ssh.Client
}

func dial(ctx context.Context, addr string, port int, keyPath string) (*SSHConn, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	c, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", addr, port), cfg)
	if err != nil {
		return nil, err
	}
	return &SSHConn{client: c}, nil
}

func (c *SSHConn) Close() { c.client.Close() }

func dialBootlooseNode(cfg config.Config, cl *cluster.Cluster, name string) (*SSHConn, error) {
	machines, err := cl.Inspect(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect bootloose cluster: %w", err)
	}

	var target *cluster.Machine
	for _, m := range machines {
		if m.Hostname() == name {
			target = m
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("bootloose machine %q not found", name)
	}

	hostPort, err := target.HostPort(22)
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH port for %q: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for {
		conn, err := dial(ctx, "localhost", hostPort, cfg.Cluster.PrivateKey)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("SSH never became ready on %q: %w", name, err)
		case <-time.After(time.Second):
		}
	}
}

func (c *SSHConn) Run(ctx context.Context, cmd string) (string, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}
