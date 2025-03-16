package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kcp-dev/multicluster-provider/envtest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	env       *envtest.Environment
	kcpConfig *rest.Config
)

func TestBuilder(t *testing.T) {
	RegisterFailHandler(Fail)

	// Start a shared kcp instance.
	var err error
	env = &envtest.Environment{AttachKcpOutput: true}
	kcpConfig, err = env.Start()
	require.NoError(t, err, "failed to start envtest environment")
	defer env.Stop()

	RunSpecs(t, "Provider Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Prevent the metrics listener being created
	metricsserver.DefaultBindAddress = "0"
})

var _ = AfterSuite(func() {
	// Put the DefaultBindAddress back
	metricsserver.DefaultBindAddress = ":8080"
})
