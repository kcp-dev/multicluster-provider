/*
Copyright 2021 The Kubernetes Authors.

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

package controlplane

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kcp-dev/multicluster-provider/envtest/internal/addr"
	"github.com/kcp-dev/multicluster-provider/envtest/internal/certs"
	"github.com/kcp-dev/multicluster-provider/envtest/internal/process"
)

var log = ctrllog.Log.WithName("controlplane")

const (
	// saKeyFile is the name of the service account signing private key file.
	saKeyFile = "sa-signer.key"
	// saKeyFile is the name of the service account signing public key (cert) file.
	saCertFile = "sa-signer.crt"
)

// SecureServing provides/configures how the API server serves on the secure port.
type SecureServing struct {
	// ListenAddr contains the host & port to serve on.
	//
	// Configurable.  If unset, it will be defaulted.
	process.ListenAddr
	// CA contains the CA that signed the API server's serving certificates.
	//
	// Read-only.
	CA []byte
	// Authn can be used to provision users, and override what type of
	// authentication is used to provision users.
	//
	// Configurable.  If unset, it will be defaulted.
	Authn
}

// EmbeddedEtcd configures the embedded etcd.
type EmbeddedEtcd struct {
	// PeerPort is the etcd peer port.
	PeerPort string
	// ClientPort is the etcd client port.
	ClientPort string
}

// Shard knows how to run a kcp shard.
type Shard struct {
	// SecureServing indicates how the kcp shard will serve on the secure port.
	//
	// Some parts are configurable.  Will be defaulted if unset.
	SecureServing

	// EmbeddedEtcd configures the embedded etcd.
	EmbeddedEtcd EmbeddedEtcd

	// Path is the path to the kcp binary.
	//
	// If this is left as the empty string, we will attempt to locate a binary,
	// by checking for the TEST_ASSET_KCP environment variable, and
	// the default test assets directory. See the "Binaries" section above (in
	// doc.go) for details.
	Path string

	// RootDir is a path to a directory containing certificates the
	// Shard will need, the Shard's embedded etcd data and the admin kubeconfig.
	//
	// If left unspecified, then the Start() method will create a fresh temporary
	// directory, and the Stop() method will clean it up.
	RootDir string

	// StartTimeout, StopTimeout specify the time the Shard is allowed to
	// take when starting and stoppping before an error is emitted.
	//
	// If not specified, these default to 1 min and 20 seconds respectively.
	StartTimeout time.Duration
	StopTimeout  time.Duration

	// Out, Err specify where Shard should write its StdOut, StdErr to.
	//
	// If not specified, the output will be discarded.
	Out io.Writer
	Err io.Writer

	processState *process.State

	// args contains the structured arguments to use for running the kcp binary.
	// Lazily initialized by .Configure(), Defaulted eventually with .defaultArgs()
	args *process.Arguments
}

// Configure returns Arguments that may be used to customize the
// flags used to launch the kcp shard.  A set of defaults will
// be applied underneath.
func (s *Shard) Configure() *process.Arguments {
	if s.args == nil {
		s.args = process.EmptyArguments()
	}
	return s.args
}

// Start starts the kcp shard, waits for it to come up, and returns an error,
// if occurred.
func (s *Shard) Start() error {
	if err := s.prepare(); err != nil {
		return err
	}
	log.Info("starting kcp server", "command", quoteArgs(append([]string{s.processState.Path}, s.processState.Args...)))
	return s.processState.Start(s.Out, s.Err)
}

func (s *Shard) prepare() error {
	if err := s.setProcessState(); err != nil {
		return err
	}
	return s.Authn.Start()
}

// configurePorts configures the serving ports for this API server.
//
// Most of this method currently deals with making the deprecated fields
// take precedence over the new fields.
func (s *Shard) configurePorts() error {
	// prefer the old fields to the new fields if a user set one,
	// otherwise, default the new fields and populate the old ones.

	// Secure: SecurePort, SecureServing
	if s.SecureServing.Port == "" || s.SecureServing.Address == "" {
		port, host, err := addr.Suggest("")
		if err != nil {
			return fmt.Errorf("unable to provision unused secure port: %w", err)
		}
		s.SecureServing.Port = strconv.Itoa(port)
		s.SecureServing.Address = host
	}

	if s.EmbeddedEtcd.PeerPort == "" {
		port, _, err := addr.Suggest("")
		if err != nil {
			return fmt.Errorf("unable to provision unused etcd peer port: %w", err)
		}
		s.EmbeddedEtcd.PeerPort = strconv.Itoa(port)
	}
	if s.EmbeddedEtcd.ClientPort == "" {
		port, _, err := addr.Suggest("")
		if err != nil {
			return fmt.Errorf("unable to provision unused etcd client port: %w", err)
		}
		s.EmbeddedEtcd.ClientPort = strconv.Itoa(port)
	}

	return nil
}

func (s *Shard) setProcessState() error {
	// unconditionally re-set this so we can successfully restart
	// TODO(directxman12): we supported this in the past, but do we actually
	// want to support re-using an API server object to restart?  The loss
	// of provisioned users is surprising to say the least.
	s.processState = &process.State{
		Dir:          s.RootDir,
		Path:         s.Path,
		StartTimeout: s.StartTimeout,
		StopTimeout:  s.StopTimeout,
	}
	if err := s.processState.Init("kcp"); err != nil {
		return err
	}

	if err := s.configurePorts(); err != nil {
		return err
	}

	// the secure port will always be on, so use that
	s.processState.HealthCheck.URL = *s.SecureServing.URL("https", "/readyz")

	s.RootDir = s.processState.Dir
	s.Path = s.processState.Path
	s.StartTimeout = s.processState.StartTimeout
	s.StopTimeout = s.processState.StopTimeout

	if err := s.populateServingCerts(); err != nil {
		return err
	}

	if s.SecureServing.Authn == nil {
		authn, err := NewCertAuthn()
		if err != nil {
			return err
		}
		s.SecureServing.Authn = authn
	}

	if err := s.Authn.Configure(s.RootDir, s.Configure()); err != nil {
		return err
	}

	// NB(directxman12): insecure port is a mess:
	// - 1.19 and below have the `--insecure-port` flag, and require it to be set to zero to
	//   disable it, otherwise the default will be used and we'll conflict.
	// - 1.20 requires the flag to be unset or set to zero, and yells at you if you configure it
	// - 1.24 won't have the flag at all...
	//
	// In an effort to automatically do the right thing during this mess, we do feature discovery
	// on the flags, and hope that we've "parsed" them properly.
	//
	// TODO(directxman12): once we support 1.20 as the min version (might be when 1.24 comes out,
	// might be around 1.25 or 1.26), remove this logic and the corresponding line in API server's
	// default args.
	if err := s.discoverFlags(); err != nil {
		return err
	}

	s.processState.Args = append([]string{"start"}, s.Configure().AsStrings(s.defaultArgs())...)

	return nil
}

// discoverFlags checks for certain flags that *must* be set in certain
// versions, and *must not* be set in others.
func (s *Shard) discoverFlags() error {
	/*
		present, err := s.processState.CheckFlag("insecure-port")
		if err != nil {
			return err
		}

		if !present {
			s.Configure().Disable("insecure-port")
		}
	*/

	return nil
}

func (s *Shard) defaultArgs() map[string][]string {
	args := map[string][]string{
		"root-directory":            {s.RootDir},
		"secure-port":               {s.SecureServing.Port},
		"bind-address":              {s.SecureServing.Address},
		"embedded-etcd-peer-port":   {s.EmbeddedEtcd.PeerPort},
		"embedded-etcd-client-port": {s.EmbeddedEtcd.ClientPort},
		"external-hostname":         {s.SecureServing.Address},
	}
	return args
}

func (s *Shard) populateServingCerts() error {
	_, statErr := os.Stat(filepath.Join(s.RootDir, "apiserver.crt"))
	if !os.IsNotExist(statErr) {
		return statErr
	}

	ca, err := certs.NewTinyCA()
	if err != nil {
		return err
	}

	servingCerts, err := ca.NewServingCert()
	if err != nil {
		return err
	}

	certData, keyData, err := servingCerts.AsBytes()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(s.RootDir, "apiserver.crt"), certData, 0640); err != nil { //nolint:gosec
		return err
	}
	if err := os.WriteFile(filepath.Join(s.RootDir, "apiserver.key"), keyData, 0640); err != nil { //nolint:gosec
		return err
	}

	s.SecureServing.CA = ca.CA.CertBytes()

	// service account signing files too
	saCA, err := certs.NewTinyCA()
	if err != nil {
		return err
	}

	saCert, saKey, err := saCA.CA.AsBytes()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(s.RootDir, saCertFile), saCert, 0640); err != nil { //nolint:gosec
		return err
	}
	return os.WriteFile(filepath.Join(s.RootDir, saKeyFile), saKey, 0640) //nolint:gosec
}

// Stop stops this process gracefully, waits for its termination, and cleans up
// the RootDir if necessary.
func (s *Shard) Stop() error {
	if s.processState != nil {
		if s.processState.DirNeedsCleaning {
			s.RootDir = "" // reset the directory if it was randomly allocated, so that we can safely restart
		}
		if err := s.processState.Stop(); err != nil {
			return err
		}
	}
	return s.Authn.Stop()
}

// PrepareShard is an internal-only (NEVER SHOULD BE EXPOSED)
// function that sets up the kcp shard just before starting it,
// without actually starting it.  This saves time on tests.
//
// NB(directxman12): do not expose this outside of internal -- it's unsafe to
// use, because things like port allocation could race even more than they
// currently do if you later call start!
func PrepareShard(s *Shard) error {
	return s.prepare()
}

func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.Contains(arg, " ") {
			quoted[i] = strconv.Quote(arg)
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
