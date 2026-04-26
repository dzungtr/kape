package k8s

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
)

// KapeConfigLoader implements ports.KapeConfigLoader.
// It reads the kape-config ConfigMap from the configured namespace.
type KapeConfigLoader struct {
	client    client.Client
	namespace string
	name      string
}

// NewKapeConfigLoader creates a new KapeConfigLoader.
func NewKapeConfigLoader(c client.Client, namespace, name string) *KapeConfigLoader {
	return &KapeConfigLoader{client: c, namespace: namespace, name: name}
}

// Load reads the kape-config ConfigMap and returns a KapeConfig with defaults applied.
func (l *KapeConfigLoader) Load(ctx context.Context) (domainconfig.KapeConfig, error) {
	var cm corev1.ConfigMap
	key := types.NamespacedName{Name: l.name, Namespace: l.namespace}
	if err := l.client.Get(ctx, key, &cm); err != nil {
		// Missing ConfigMap is non-fatal — return defaults.
		return domainconfig.KapeConfig{}.WithDefaults(), nil
	}

	cfg := domainconfig.KapeConfig{
		HandlerImage:           cm.Data["kapehandler.image"],
		HandlerImageVersion:    cm.Data["kapehandler.version"],
		KapetoolImage:          cm.Data["kapetool.image"],
		KapetoolImageVersion:   cm.Data["kapetool.version"],
		NATSMonitoringEndpoint: cm.Data["nats.monitoringEndpoint"],
		NATSStreamName:         cm.Data["nats.streamName"],
		ClusterName:            cm.Data["cluster.name"],
		QdrantVersion:          cm.Data["qdrant.version"],
		QdrantStorageClass:     cm.Data["qdrant.storageClass"],
	}

	if raw := cm.Data["handler.maxIterations"]; raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return domainconfig.KapeConfig{}, fmt.Errorf("parsing handler.maxIterations %q: %w", raw, err)
		}
		cfg.DefaultMaxIterations = int32(n)
	}

	return cfg.WithDefaults(), nil
}
