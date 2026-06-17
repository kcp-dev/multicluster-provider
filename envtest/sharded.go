/*
Copyright 2026 The kcp Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package envtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	envShardedTestServer = "KCP_ASSET_SHARDED_TEST_SERVER"
	envNumberOfShards    = "KCP_NUMBER_OF_SHARDS"
	envTestWorkDir       = "KCP_TEST_WORK_DIR"

	defaultNumberOfShards = 2
	defaultStartTimeout   = 2 * time.Minute
	defaultStopTimeout    = 20 * time.Second

	logSubdir   = ".kcp-sharded-test-server"
	logFileName = "envtest.log"

	readyFileName     = "ready-to-test"
	kcpDir            = ".kcp"
	adminKubeconfig   = "admin.kubeconfig"
	kubeconfigContext = "base"
)

// Sharded runs kcp's sharded-test-server as a subprocess. It is the
// recommended way to drive multicluster-provider e2e tests against a
// realistic multi-shard kcp topology.
//
// Set the desired fields on a zero value, call Start with a context, and
// retrieve the admin rest.Config via Config. Use Stop (or t.Cleanup) to
// terminate the subprocess.
//
// To run the following binaries must be available either in PATH or set
// in the respective environment variables:
//
//	sharded-test-server KCP_ASSET_SHARDED_TEST_SERVER
//	kcp                 KCP_ASSET_KCP
//	kcp-front-proxy     KCP_ASSET_KCP_FRONT_PROXY
//	cache-server        KCP_ASSET_CACHE_SERVER
type Sharded struct {
	// NumberOfShards is passed to -number-of-shards. Defaults to
	// $KCP_NUMBER_OF_SHARDS if set, otherwise 2.
	NumberOfShards int

	// WorkDir is passed to -work-dir-path. Defaults to $KCP_TEST_WORK_DIR
	// if set, otherwise the current working directory.
	WorkDir string

	// ExtraArgs are appended to the sharded-test-server invocation. Use for
	// --proxy-*, --shard-*, --quiet, etc.
	ExtraArgs []string

	// StartTimeout is how long Start waits for the readiness sentinel.
	// Defaults to $TEST_KCP_START_TIMEOUT or 2m.
	StartTimeout time.Duration

	// StopTimeout is how long Stop waits for the subprocess after SIGTERM
	// before sending SIGKILL. Defaults to $TEST_KCP_STOP_TIMEOUT or 20s.
	StopTimeout time.Duration

	cmd     *exec.Cmd
	logFile *os.File
	config  *rest.Config
	started bool

	// waitDone closes after the single Wait goroutine in Start records the
	// subprocess exit. waitErr holds that error. Both fields are read by
	// waitReady and Stop; only the Wait goroutine writes them, so the
	// happens-before relationship is established by waitDone closing.
	waitDone chan struct{}
	waitErr  error
}

// Start launches sharded-test-server and blocks until it is ready or ctx is
// cancelled. Start must be called at most once per Sharded instance.
func (s *Sharded) Start(ctx context.Context) error {
	if s.started {
		return errors.New("envtest: Sharded.Start called twice")
	}
	s.started = true

	if err := s.applyDefaults(); err != nil {
		return err
	}

	binary, err := resolveShardedTestServer()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(s.WorkDir, logSubdir), 0o755); err != nil {
		return fmt.Errorf("envtest: create log dir: %w", err)
	}
	logPath := filepath.Join(s.WorkDir, logSubdir, logFileName)
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("envtest: open log file %s: %w", logPath, err)
	}
	s.logFile = logF

	//nolint:prealloc
	args := []string{
		"-number-of-shards", strconv.Itoa(s.NumberOfShards),
		"-work-dir-path", s.WorkDir,
	}
	args = append(args, s.ExtraArgs...)

	cmd := exec.Command(binary, args...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	setProcessGroup(cmd)
	s.cmd = cmd

	if err := cmd.Start(); err != nil {
		_ = logF.Close()
		s.logFile = nil
		return fmt.Errorf("envtest: start sharded-test-server: %w", err)
	}

	s.waitDone = make(chan struct{})
	go func() {
		s.waitErr = s.cmd.Wait()
		close(s.waitDone)
	}()

	if err := s.waitReady(ctx); err != nil {
		_ = s.Stop()
		return err
	}

	kubeconfigPath := filepath.Join(s.WorkDir, kcpDir, adminKubeconfig)
	cfg, err := loadAdminConfig(kubeconfigPath)
	if err != nil {
		_ = s.Stop()
		return fmt.Errorf("envtest: load %s: %w", kubeconfigPath, err)
	}
	s.config = cfg
	return nil
}

func (s *Sharded) waitReady(ctx context.Context) error {
	readyPath := filepath.Join(s.WorkDir, kcpDir, readyFileName)
	logPath := filepath.Join(s.WorkDir, logSubdir, logFileName)

	timeoutCtx, cancel := context.WithTimeout(ctx, s.StartTimeout)
	defer cancel()

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	for {
		if _, err := os.Stat(readyPath); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("envtest: stat %s: %w", readyPath, err)
		}

		select {
		case <-s.waitDone:
			if s.waitErr != nil {
				return fmt.Errorf("envtest: sharded-test-server exited before ready: %w (see %s)", s.waitErr, logPath)
			}
			return fmt.Errorf("envtest: sharded-test-server exited before ready (see %s)", logPath)
		case <-timeoutCtx.Done():
			return fmt.Errorf("envtest: sharded-test-server not ready within %s: %w (see %s)", s.StartTimeout, timeoutCtx.Err(), logPath)
		case <-tick.C:
		}
	}
}

// Stop terminates the subprocess (SIGTERM, then SIGKILL after StopTimeout)
// and closes the subprocess log file. Stop is idempotent and not safe for
// concurrent calls.
func (s *Sharded) Stop() error {
	if s.cmd == nil || s.cmd.Process == nil {
		s.closeLog()
		return nil
	}

	// If the wait goroutine never started (Start failed before launching it),
	// fall back to closing the log file. This shouldn't happen under normal
	// flow but keeps Stop safe to call from any error path.
	if s.waitDone == nil {
		s.closeLog()
		return nil
	}

	// If the subprocess already exited (e.g. waitReady saw it die), there's
	// nothing left to signal — just clean up.
	select {
	case <-s.waitDone:
		s.cmd.Process = nil
		s.closeLog()
		if s.waitErr != nil && !isSignalExit(s.waitErr) {
			return s.waitErr
		}
		return nil
	default:
	}

	pgid := s.cmd.Process.Pid
	_ = terminate(pgid)

	select {
	case <-s.waitDone:
		s.cmd.Process = nil
		s.closeLog()
		if s.waitErr != nil && !isSignalExit(s.waitErr) {
			return s.waitErr
		}
		return nil
	case <-time.After(s.StopTimeout):
		_ = kill(pgid)
		<-s.waitDone
		s.cmd.Process = nil
		s.closeLog()
		return fmt.Errorf("envtest: sharded-test-server did not exit within %s, killed", s.StopTimeout)
	}
}

func (s *Sharded) closeLog() {
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

// Config returns the admin rest.Config for the front-proxy, or nil if Start
// has not succeeded. The same pointer is returned on each call; copy it
// with rest.CopyConfig before mutating.
func (s *Sharded) Config() *rest.Config {
	return s.config
}

func (s *Sharded) applyDefaults() error {
	if s.NumberOfShards == 0 {
		if v := os.Getenv(envNumberOfShards); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("envtest: parse %s=%q: %w", envNumberOfShards, v, err)
			}
			s.NumberOfShards = n
		} else {
			s.NumberOfShards = defaultNumberOfShards
		}
	}

	if s.WorkDir == "" {
		if v := os.Getenv(envTestWorkDir); v != "" {
			s.WorkDir = v
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("envtest: getwd: %w", err)
			}
			s.WorkDir = cwd
		}
	}

	if s.StartTimeout == 0 {
		if v := os.Getenv(envStartTimeout); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("envtest: parse %s=%q: %w", envStartTimeout, v, err)
			}
			s.StartTimeout = d
		} else {
			s.StartTimeout = defaultStartTimeout
		}
	}

	if s.StopTimeout == 0 {
		if v := os.Getenv(envStopTimeout); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("envtest: parse %s=%q: %w", envStopTimeout, v, err)
			}
			s.StopTimeout = d
		} else {
			s.StopTimeout = defaultStopTimeout
		}
	}

	return nil
}

func resolveShardedTestServer() (string, error) {
	if v := os.Getenv(envShardedTestServer); v != "" {
		return v, nil
	}
	p, err := exec.LookPath("sharded-test-server")
	if err != nil {
		return "", fmt.Errorf("envtest: locate sharded-test-server (set %s or add to PATH): %w", envShardedTestServer, err)
	}
	return p, nil
}

func loadAdminConfig(path string) (*rest.Config, error) {
	raw, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	return clientcmd.NewNonInteractiveClientConfig(*raw, kubeconfigContext, nil, nil).ClientConfig()
}
