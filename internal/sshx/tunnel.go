package sshx

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Tunnel forwards a remote DB port to a local ephemeral port.
type Tunnel struct {
	client   *ssh.Client
	listener net.Listener
	LocalPort int
	lnAddr   string
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func authMethods(keyPath, passphrase, password string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	// ssh-agent first (also covers Pageant via SSH_AUTH_SOCK when set)
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			ac := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(ac.Signers))
		}
	}
	if keyPath != "" {
		keyPath = expandPath(keyPath)
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", keyPath, err)
		}
		var signer ssh.Signer
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, fmt.Errorf("parse ssh key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}
	return methods, nil
}

// Dial establishes client + local forward to remoteHost:remotePort.
// remoteHost is resolved FROM the server (use 127.0.0.1 to keep PG private).
func Dial(sshHost string, sshPort int, sshUser, keyPath, passphrase, password, remoteHost string, remotePort int, timeout time.Duration) (*Tunnel, error) {
	if sshPort == 0 {
		sshPort = 22
	}
	methods, err := authMethods(keyPath, passphrase, password)
	if err != nil {
		return nil, err
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no ssh auth method: pick a key file, enter the ssh password, or run ssh-agent")
	}
	cfg := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // MVP: TODO known_hosts + UI pinning
		Timeout:         timeout,
	}
	addr := fmt.Sprintf("%s:%d", sshHost, sshPort)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return nil, err
	}
	t := &Tunnel{client: client, listener: ln, LocalPort: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer local.Close()
				remote, err := client.Dial("tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
				if err != nil {
					return
				}
				defer remote.Close()
				go copyConn(local, remote)
				copyConn(remote, local)
			}()
		}
	}()
	return t, nil
}

func copyConn(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		src.SetDeadline(time.Now().Add(2 * time.Minute))
		n, err := src.Read(buf)
		if n > 0 {
			dst.SetDeadline(time.Now().Add(2 * time.Minute))
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *Tunnel) Close() {
	if t.listener != nil {
		t.listener.Close()
	}
	if t.client != nil {
		t.client.Close()
	}
}

// RunCommand executes one command over SSH and returns combined output.
// Read-only by convention — callers must only pass inspection commands.
func RunCommand(sshHost string, sshPort int, sshUser, keyPath, passphrase, password, command string, timeout time.Duration) (string, error) {
	methods, err := authMethods(keyPath, passphrase, password)
	if err != nil {
		return "", err
	}
	if len(methods) == 0 {
		return "", fmt.Errorf("no ssh auth method")
	}
	cfg := &ssh.ClientConfig{User: sshUser, Auth: methods, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: timeout}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshHost, sshPort), cfg)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(command)
	return strings.TrimSpace(string(out)), err
}

// TestSSH runs a fast handshake + `echo ok` to prove the tunnel path works.
func TestSSH(sshHost string, sshPort int, sshUser, keyPath, passphrase, password string, timeout time.Duration) (string, error) {
	methods, err := authMethods(keyPath, passphrase, password)
	if err != nil {
		return "", err
	}
	if len(methods) == 0 {
		return "", fmt.Errorf("no ssh auth method: pick a key file, enter the ssh password, or run ssh-agent")
	}
	cfg := &ssh.ClientConfig{User: sshUser, Auth: methods, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: timeout}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshHost, sshPort), cfg)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput("echo ok && uname -srm")
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}
