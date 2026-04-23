package integration

import (
	"context"
	"fmt"
	"os"
	"time"

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

func (c *SSHConn) Run(ctx context.Context, cmd string) (string, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}
