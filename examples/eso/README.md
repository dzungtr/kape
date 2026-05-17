# ESO Reference Manifests

Reference manifests for sourcing KapeTool connection Secrets from
[External Secrets Operator](https://external-secrets.io) (ESO). Apply these
to let ESO sync secrets from an external vault into Kubernetes, so the kape
operator can mount them into handler Deployments.

## Prerequisites

- ESO CRDs installed in the cluster. Follow the [ESO install guide](https://external-secrets.io/latest/introduction/getting-started/).
- The `SecretStore` must be authenticated to its backend before `ExternalSecret`
  resources will sync. This example uses Vault with Kubernetes auth: the pod's
  ServiceAccount must be bound to the Vault role named `kape-system`, and Vault
  must have Kubernetes auth enabled on `mountPath: "kubernetes"`.

**Vault Kubernetes auth — ServiceAccount note:** The example `SecretStore` does
not set `auth.kubernetes.serviceAccountRef`. ESO will use its own controller
pod's ServiceAccount token to authenticate to Vault. The Vault Kubernetes role
(`kape-system` in this example) must therefore be bound to the ESO controller's
ServiceAccount (typically `external-secrets` in the `external-secrets` namespace
— check `kubectl get sa -n external-secrets`). If you prefer a dedicated
ServiceAccount, add `serviceAccountRef.name` under `auth.kubernetes` and create
the SA in `kape-system`:

```yaml
auth:
  kubernetes:
    mountPath: "kubernetes"
    role: "kape-system"
    serviceAccountRef:
      name: kape-eso-sa   # SA must exist in kape-system namespace
```

## Apply the manifests

> **Note:** All commands below must be run from the **repository root** (the directory containing `go.work`), unless otherwise stated.

```bash
kubectl apply -f examples/eso/secretstore.yaml
kubectl apply -f examples/eso/externalsecret.yaml
```

## Naming convention

`target.name` (and the `ExternalSecret` metadata name) must follow the pattern:

```
kape-tool-<tool-name>-conn
```

where `<tool-name>` matches the `KapeTool` resource name exactly. The operator
looks up this Secret by that name when building the handler Deployment volume
mount. For a KapeTool named `order-memory`, the Secret name is
`kape-tool-order-memory-conn`.

## Expected Secret keys

The synced Secret must contain exactly two keys:

| Key | Description | Example value |
|---|---|---|
| `qdrant_url` | Qdrant HTTP endpoint | `https://qdrant.example.com:6333` |
| `qdrant_collection` | Collection name | `incidents` |

Both keys are read by the handler runtime at startup. Missing either key causes
the handler pod to fail its readiness check.

## Adapting to other backends

Only the `provider:` stanza in `secretstore.yaml` needs to change. The
`ExternalSecret` and everything else stay the same.

**AWS Secrets Manager**

```yaml
spec:
  provider:
    aws:
      service: SecretsManager
      region: us-east-1
      auth:
        secretRef:
          accessKeyIDSecretRef:
            name: aws-credentials
            key: access-key-id
          secretAccessKeySecretRef:
            name: aws-credentials
            key: secret-access-key
```

**GCP Secret Manager**

```yaml
spec:
  provider:
    gcpsm:
      projectID: my-gcp-project
      auth:
        workloadIdentity:
          clusterLocation: us-central1
          clusterName: my-cluster
          serviceAccountRef:
            name: kape-eso-sa
```

## Verification

After applying, ESO should sync the Secret within `refreshInterval`. Check:

```bash
kubectl get externalsecret kape-tool-order-memory-conn -n kape-system
# READY=True means the Secret was synced successfully

kubectl get secret kape-tool-order-memory-conn -n kape-system -o yaml
# data: should contain qdrant_url and qdrant_collection (base64-encoded)
```

If `READY=False`, inspect the ESO controller logs:

```bash
kubectl logs -l app.kubernetes.io/name=external-secrets -n external-secrets-system -f
```
