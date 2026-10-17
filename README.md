# cert-manager webhook for PrimeDNS

A [cert-manager](https://cert-manager.io) DNS-01 solver for zones hosted at [PrimeDNS](https://primedns.net).
It creates and removes the `_acme-challenge` TXT record through the PrimeDNS ACME endpoint
(`POST` / `DELETE /v1/dns/acme-challenge`), so wildcard certificates and certificates for hosts that are
not reachable from the internet work the same way as everywhere else.

## How it works

cert-manager calls the webhook with the record name it resolved (after following a CNAME, if there is one).
The webhook sends that name and the challenge value to the PrimeDNS API with your API key. Nothing else
in your DNS zone is touched.

## 1. Create an API key

In the PrimeDNS panel (Account, API Keys) choose **Certificate key** and list the hostnames you will issue
certificates for. For `example.com` and `*.example.com` that is one name: the key gets the scope
`acme:name:_acme-challenge.example.com`. A key with this scope can write that one TXT name and nothing else.

## 2. Install the webhook

```bash
helm install primedns-webhook oci://ghcr.io/primedns/charts/primedns-webhook \
  --version 0.1.0 --namespace cert-manager
```

The chart installs the webhook next to cert-manager. `groupName` (`acme.primedns.net`) is fixed by the chart
and used in the issuer below. Requires cert-manager v1.x.

## 3. Store the key and create an issuer

```bash
kubectl -n cert-manager create secret generic primedns-api-key \
  --from-literal=api-key='pdns_...'
```

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-primedns
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: you@example.com
    privateKeySecretRef:
      name: letsencrypt-primedns-account
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

For a namespaced `Issuer` the Secret must be in the same namespace as the issuer; for a `ClusterIssuer` it is
read from cert-manager's cluster resource namespace (`cert-manager` by default).

Use `https://acme-staging-v02.api.letsencrypt.org/directory` first to test without hitting rate limits.

## 4. Request a certificate

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: example-com
spec:
  secretName: example-com-tls
  issuerRef:
    name: letsencrypt-primedns
    kind: ClusterIssuer
  dnsNames:
    - example.com
    - "*.example.com"
```

```bash
kubectl get certificate example-com
kubectl describe challenge          # the error from the API is shown here when a challenge fails
```

## CNAME delegation

If `_acme-challenge.app.example.org` is a CNAME to `_acme-challenge.app.example.com`, and `example.com` is
hosted at PrimeDNS, set `cnameStrategy: Follow` in the solver (as in the issuer above). cert-manager then follows
the CNAME and gives the webhook the **target** name. Without it cert-manager uses the original name and the API
refuses it (403 `NAME_NOT_ALLOWED`). Give the key the scope for the target
(`acme:name:_acme-challenge.app.example.com`).

## Configuration

| Field | Required | Description |
|---|---|---|
| `apiKeySecretRef.name`, `.key` | yes | Secret and key that hold the PrimeDNS API key |
| `apiUrl` | no | API base URL, default `https://api.primedns.net/v1` |

## Errors

| Message | Meaning |
|---|---|
| `HTTP 401 ... the key is wrong, revoked or expired` | check the key in the Secret |
| `HTTP 403 NAME_NOT_ALLOWED` / `INSUFFICIENT_SCOPE` | the key has no scope for this exact name |
| `HTTP 429` / `5xx` | the webhook retries once, then reports the error to cert-manager |
| `NAME_NOT_ALLOWED` on a CNAME-delegated name | add `cnameStrategy: Follow` to the solver and give the key the scope for the CNAME target |

## Build it yourself

You do not have to run our image. The code is small and open (Apache-2.0), and the `Dockerfile` in this repository builds
it from public sources:

```bash
git clone https://github.com/PrimeDNS/cert-manager-webhook-primedns
cd cert-manager-webhook-primedns
docker build -t registry.example.com/primedns-webhook:0.1.0 .
helm install primedns-webhook ./deploy/primedns-webhook --namespace cert-manager \
  --set image.repository=registry.example.com/primedns-webhook --set image.tag=0.1.0
```

The webhook only talks to the public PrimeDNS API (`POST` and `DELETE /v1/dns/acme-challenge`). If you use our published
image, pin it by digest instead of a tag. The first build compiles the Kubernetes libraries and takes a while.

## Development

```bash
go test ./...                                   # unit tests
PRIMEDNS_TEST_API_KEY=pdns_... PRIMEDNS_TEST_HOST=host.example.com \
  go test -run Live -v ./...                    # round trip against the real API (needs dig)
docker build -t primedns-webhook:dev .
```

The live test needs a key with the scope for `_acme-challenge.<host>` in a zone of that key's account.

## License

Apache License 2.0 (see `LICENSE`).
