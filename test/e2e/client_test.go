package e2e

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kcp-dev/kcp/sdk/apis/core"

	clusterclient "github.com/kcp-dev/multicluster-provider/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster Client", Ordered, func() {
	It("can access the root cluster", func(ctx context.Context) {
		cli, err := clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		rootCli, err := cli.Cluster(core.RootCluster.Path())
		Expect(err).NotTo(HaveOccurred())

		ns := &corev1.Namespace{}
		err = rootCli.Get(ctx, client.ObjectKey{Name: "default"}, ns)
		Expect(err).NotTo(HaveOccurred())
	})
})
