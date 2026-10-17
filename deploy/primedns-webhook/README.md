# primedns-webhook

[cert-manager](https://cert-manager.io) DNS-01 webhook solver for zones hosted at [PrimeDNS](https://primedns.net).
It creates and removes the `_acme-challenge` TXT record through the PrimeDNS API, so wildcard certificates and certificates for
hosts that are not reachable from the internet work the same way as with the built-in providers.

## Install

```bash
helm install primedns-webhook oci://ghcr.io/primedns/charts/primedns-webhook \
  --namespace cert-manager
```

Requires cert-manager. The webhook runs as a non-root user with a read-only root filesystem.

## Use

Create an API key in the PrimeDNS panel (Account, API Keys, "Certificate key") and store it in a Secret:

```bash
kubectl -n cert-manager create secret generic primedns-api-key --from-literal=api-key='pdns_...'
```

Reference it from the issuer:

```yaml
solvers:
  - dns01:
      cnameStrategy: Follow
      webhook:
        groupName: acme.primedns.net
        solverName: primedns
        config:
          apiKeySecretRef:
            name: primedns-api-key
            key: api-key
```

`cnameStrategy: Follow` is only needed when `_acme-challenge` is delegated with a CNAME.

## Values

| Value | Default | Description |
|---|---|---|
| `groupName` | `acme.primedns.net` | API group of the webhook; use the same value as `groupName` in the issuer |
| `image.repository` | `ghcr.io/primedns/cert-manager-webhook-primedns` | Use your own registry if you build the image yourself |
| `image.tag` | chart appVersion | Image tag |
| `resources` | small requests, 128Mi memory limit | Container resources |

Full documentation, the issuer and certificate examples, error messages and how to build your own image:
https://github.com/PrimeDNS/cert-manager-webhook-primedns
